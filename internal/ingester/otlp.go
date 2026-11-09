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

package ingester

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpdeploy"
)

// maxOTLPBodyBytes bounds both the wire size and the decompressed size of an
// OTLP/HTTP trace export. Exporters batch many spans per request, so this is
// larger than maxWebhookBodyBytes; the decompressed bound also defeats gzip
// bombs.
const maxOTLPBodyBytes = 8 << 20 // 8 MiB

// serveOTLP implements the OTLP/HTTP traces receiver contract
// (POST /v1/traces, protobuf or JSON, optionally gzip-compressed) so an
// OpenTelemetry Collector's otlphttp exporter can point straight at this
// handler. Spans selected by the configured mapping become DeployEvents.
func (h *Handler) serveOTLP(w http.ResponseWriter, r *http.Request) {
	if h.otlpErr != nil {
		h.logger.Error("ingester: otlp source is misconfigured", "err", h.otlpErr)
		http.Error(w, "otlp source misconfigured", http.StatusInternalServerError)
		return
	}

	format, ok := otlpdeploy.FormatFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		http.Error(w, "unsupported media type: use application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}

	var body io.Reader = http.MaxBytesReader(w, r.Body, maxOTLPBodyBytes)
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad request: invalid gzip body", http.StatusBadRequest)
			return
		}
		defer func() { _ = zr.Close() }()
		body = io.LimitReader(zr, maxOTLPBodyBytes+1)
	default:
		http.Error(w, "unsupported content-encoding "+enc, http.StatusUnsupportedMediaType)
		return
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(raw) > maxOTLPBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	spans, err := otlpdeploy.Decode(raw, format)
	if err != nil {
		h.logger.Error("ingester: failed to decode OTLP request", "err", err)
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	res := h.otlp.Events(spans, time.Now())
	stored := 0
	for _, ev := range res.Events {
		ev.Source = h.source.Name
		if ev.Cluster == "" {
			ev.Cluster = h.source.ClusterName
		}
		if h.source.AppName != "" && ev.App != h.source.AppName {
			continue
		}
		h.store.Add(ev)
		stored++
		h.logger.Info("ingester: deploy event stored",
			"id", ev.ID, "app", ev.App, "revision", ev.Revision, "timestamp", ev.Timestamp)
	}
	if res.Rejected > 0 {
		h.logger.Warn("ingester: OTLP spans matched but were rejected",
			"rejected", res.Rejected, "reason", res.RejectReason)
	}

	// OTLP/HTTP requires 200 with an Export*ServiceResponse body; a 2xx
	// with partial_success tells the exporter not to retry rejected spans.
	w.Header().Set("Content-Type", format.ContentType())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(otlpdeploy.EncodeResponse(format, res.Rejected, res.RejectReason))
}
