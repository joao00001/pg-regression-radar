// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package otlpexport publishes detected PerformanceRegressions as OTLP/HTTP
// log records (POST <endpoint>/v1/logs), so any OpenTelemetry Collector or
// OTLP-capable backend can receive them next to the rest of its telemetry.
//
// It is hand-rolled on protowire (and encoding/json for the JSON encoding)
// rather than the OpenTelemetry SDK: the surface is one request type, and
// this keeps the dependency tree unchanged. Delivery is asynchronous and
// bounded: Export never blocks the detection loop, a full queue drops (and
// counts) instead of growing, and transient failures are retried with
// backoff.
package otlpexport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Encoding selects the OTLP/HTTP payload format.
type Encoding string

const (
	EncodingProtobuf Encoding = "protobuf"
	EncodingJSON     Encoding = "json"
)

const (
	defaultQueueSize   = 256
	defaultTimeout     = 10 * time.Second
	defaultMaxAttempts = 4
	defaultBackoff     = 500 * time.Millisecond
	maxBackoff         = 30 * time.Second
	logsPath           = "/v1/logs"
	maxResponseDrain   = 64 << 10
)

// Config configures an Exporter.
type Config struct {
	// Endpoint is the OTLP/HTTP base URL ("http://collector:4318") or the
	// full logs URL; "/v1/logs" is appended unless already present.
	Endpoint string
	// Encoding defaults to protobuf.
	Encoding Encoding
	// Headers are sent on every request (e.g. Authorization).
	Headers map[string]string
	// Timeout bounds each HTTP attempt. Default 10s.
	Timeout time.Duration
	// Gzip compresses request bodies.
	Gzip bool
	// QueueSize bounds buffered regressions. Default 256.
	QueueSize int
	// MaxAttempts is the total tries per record (first try included).
	// Default 4.
	MaxAttempts int
	// BaseBackoff is the first retry delay, doubled per attempt. Default 500ms.
	BaseBackoff time.Duration
	// ResourceAttributes are added to the OTLP Resource alongside
	// service.name and service.version.
	ResourceAttributes map[string]string
	// ServiceVersion is reported as service.version and scope version.
	ServiceVersion string
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
	Logger     *slog.Logger
	// Registerer, when set, receives the pg_regression_radar_otlp_export_total counter.
	Registerer prometheus.Registerer
}

// NormalizeEndpoint validates raw and returns the full OTLP logs URL.
func NormalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("otlp export endpoint is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid otlp export endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("otlp export endpoint must use http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("otlp export endpoint has no host")
	}
	if u.User != nil {
		return "", errors.New("otlp export endpoint must not embed credentials; use headers instead")
	}
	u.Fragment = ""
	if !strings.HasSuffix(u.Path, logsPath) {
		u.Path = strings.TrimRight(u.Path, "/") + logsPath
	}
	return u.String(), nil
}

// ParseHeaders parses "k=v,k2=v2" (the OTEL_EXPORTER_OTLP_HEADERS format;
// values may be percent-encoded) into a map. Newlines are tolerated between
// entries so a multi-line Secret value works too.
func ParseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid header entry %q: want key=value", part)
		}
		if dv, err := url.PathUnescape(strings.TrimSpace(v)); err == nil {
			v = dv
		}
		if strings.ContainsAny(k, " \t:") || strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("invalid header %q", k)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

// Exporter delivers PerformanceRegressions to an OTLP/HTTP logs endpoint.
type Exporter struct {
	url      string
	cfg      Config
	client   *http.Client
	resource []attr
	logger   *slog.Logger
	counter  *prometheus.CounterVec

	mu     sync.RWMutex
	closed bool
	queue  chan v1alpha1.PerformanceRegression

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	now    func() time.Time
}

// New validates cfg and starts the delivery worker.
func New(cfg Config) (*Exporter, error) {
	endpoint, err := NormalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	switch cfg.Encoding {
	case "":
		cfg.Encoding = EncodingProtobuf
	case EncodingProtobuf, EncodingJSON:
	default:
		return nil, fmt.Errorf("unsupported otlp export encoding %q (want protobuf or json)", cfg.Encoding)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = defaultBackoff
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ServiceVersion == "" {
		cfg.ServiceVersion = "unknown"
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	// Never follow redirects: a collector (or whatever answers in its
	// place) must not be able to bounce the request, headers included,
	// to a host the operator didn't configure.
	c := *client
	c.Timeout = cfg.Timeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resource := []attr{
		{"service.name", "pg-regression-radar"},
		{"service.version", cfg.ServiceVersion},
	}
	extra := map[string]string{}
	for k, v := range cfg.ResourceAttributes {
		if v != "" && k != "service.name" && k != "service.version" {
			extra[k] = v
		}
	}
	resource = append(resource, sortedAttrs(extra)...)

	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "pg_regression_radar",
		Name:      "otlp_export_total",
		Help:      "PerformanceRegressions handed to the OTLP log exporter, by outcome (success, failed, dropped).",
	}, []string{"result"})
	for _, r := range []string{"success", "failed", "dropped"} {
		counter.WithLabelValues(r)
	}
	if cfg.Registerer != nil {
		if err := cfg.Registerer.Register(counter); err != nil {
			return nil, fmt.Errorf("register otlp export metrics: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	e := &Exporter{
		url: endpoint, cfg: cfg, client: &c, resource: resource, logger: cfg.Logger,
		counter: counter, queue: make(chan v1alpha1.PerformanceRegression, cfg.QueueSize),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), now: time.Now,
	}
	go e.run()
	return e, nil
}

// URL is the resolved logs endpoint.
func (e *Exporter) URL() string { return e.url }

// Export enqueues res without blocking. It reports false when the record was
// dropped because the queue is full or the exporter is closed.
func (e *Exporter) Export(res v1alpha1.PerformanceRegression) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		e.counter.WithLabelValues("dropped").Inc()
		return false
	}
	select {
	case e.queue <- res:
		return true
	default:
		e.counter.WithLabelValues("dropped").Inc()
		e.logger.Warn("otlpexport: queue full, dropping regression", "regression", res.Name, "endpoint", e.url)
		return false
	}
}

// Close stops accepting records and waits for queued ones to be delivered or
// for ctx to expire, whichever is first; leftovers are counted as dropped.
func (e *Exporter) Close(ctx context.Context) error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.mu.Unlock()

	select {
	case <-e.done:
		e.cancel()
		return nil
	case <-ctx.Done():
		e.cancel()
		<-e.done
		return ctx.Err()
	}
}

func (e *Exporter) run() {
	defer close(e.done)
	for res := range e.queue {
		if e.ctx.Err() != nil {
			e.counter.WithLabelValues("dropped").Inc()
			continue
		}
		if err := e.deliver(res); err != nil {
			e.counter.WithLabelValues("failed").Inc()
			e.logger.Error("otlpexport: giving up on regression", "regression", res.Name, "endpoint", e.url, "err", err)
			continue
		}
		e.counter.WithLabelValues("success").Inc()
	}
}

func (e *Exporter) payload(res v1alpha1.PerformanceRegression) (body []byte, contentType string, err error) {
	rec := buildRecord(res, e.now())
	if e.cfg.Encoding == EncodingJSON {
		body, err = encodeJSON(e.resource, e.cfg.ServiceVersion, rec)
		return body, "application/json", err
	}
	return encodeProtobuf(e.resource, e.cfg.ServiceVersion, rec), "application/x-protobuf", nil
}

func (e *Exporter) deliver(res v1alpha1.PerformanceRegression) error {
	body, contentType, err := e.payload(res)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if e.cfg.Gzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body)
		if err := zw.Close(); err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		body = buf.Bytes()
	}

	var lastErr error
	delay := e.cfg.BaseBackoff
	for attempt := 1; attempt <= e.cfg.MaxAttempts; attempt++ {
		retryable, retryAfter, err := e.post(body, contentType)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == e.cfg.MaxAttempts {
			break
		}
		wait := delay
		if retryAfter > wait {
			wait = retryAfter
		}
		if wait > maxBackoff {
			wait = maxBackoff
		}
		e.logger.Warn("otlpexport: delivery failed, will retry", "attempt", attempt, "in", wait, "err", err)
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-e.ctx.Done():
			t.Stop()
			return fmt.Errorf("shutdown while retrying: %w", lastErr)
		}
		delay *= 2
	}
	return lastErr
}

// post performs one attempt. retryable follows the OTLP/HTTP spec: network
// errors and 429/502/503/504 are retried, every other non-2xx is permanent.
func (e *Exporter) post(body []byte, contentType string) (retryable bool, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", contentType)
	if e.cfg.Gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "pg-regression-radar/"+e.cfg.ServiceVersion)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return e.ctx.Err() == nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseDrain))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, 0, nil
	}
	msg := strings.TrimSpace(string(snippet))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	err = fmt.Errorf("collector returned %s: %s", resp.Status, msg)
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true, parseRetryAfter(resp.Header.Get("Retry-After"), e.now()), err
	}
	return false, 0, err
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
