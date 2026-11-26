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

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"k8s.io/apimachinery/pkg/util/httpstream" //nolint:staticcheck // SA1019: client-go still reports upgrade failures with this package's error types
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

// Dialer opens a forward to a target and keeps it until the context ends or the
// forward breaks. up is called once, with the local port, when it is ready.
type Dialer interface {
	Forward(ctx context.Context, t Target, up func(localPort int)) error
}

// KubeDialer forwards through the Kubernetes API server with the user's own
// credentials, as `kubectl port-forward` does: a WebSocket tunnel first, and the
// older SPDY upgrade when the cluster does not offer it.
type KubeDialer struct {
	Config *rest.Config
	Kube   kubernetes.Interface
}

// Forward implements Dialer. The local end listens on 127.0.0.1 only, on a port
// the operating system picks.
func (d *KubeDialer) Forward(ctx context.Context, t Target, up func(int)) error {
	req := d.Kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(t.Namespace).Name(t.Pod).SubResource("portforward")
	rt, upgrader, err := spdy.RoundTripperFor(d.Config)
	if err != nil {
		return fmt.Errorf("preparing the port-forward: %w", err)
	}
	spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: rt}, http.MethodPost, req.URL())
	wsDialer, err := portforward.NewSPDYOverWebsocketDialer(req.URL(), d.Config)
	if err != nil {
		return fmt.Errorf("preparing the port-forward: %w", err)
	}
	dialer := portforward.NewFallbackDialer(wsDialer, spdyDialer, shouldFallBack)

	stop, ready := make(chan struct{}), make(chan struct{})
	pf, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", t.Port)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("preparing the port-forward: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- pf.ForwardPorts() }()

	select {
	case <-ready:
		ports, err := pf.GetPorts()
		if err != nil || len(ports) == 0 {
			close(stop)
			<-done
			return fmt.Errorf("the port-forward is ready but has no local port: %v", err)
		}
		up(int(ports[0].Local))
	case err := <-done:
		return forwardError(err, t)
	case <-ctx.Done():
		close(stop)
		<-done
		return nil
	}
	select {
	case err := <-done:
		return forwardError(err, t)
	case <-ctx.Done():
		close(stop)
		<-done
		return nil
	}
}

// shouldFallBack says whether a failed WebSocket tunnel should be retried over
// SPDY. client-go's WebSocket dialer reports its upgrade failure with the type
// from k8s.io/streaming, while apimachinery has its own, identical one; checking
// only one of them would silently disable the fallback on clusters that do not
// offer the tunnel (the fake API server test in forward_test.go guards this).
func shouldFallBack(err error) bool {
	return streamhttp.IsUpgradeFailure(err) || httpstream.IsUpgradeFailure(err) ||
		streamhttp.IsHTTPSProxyError(err) || httpstream.IsHTTPSProxyError(err)
}

// forwardError says what a failed forward most likely means, in the user's terms.
func forwardError(err error, t Target) error {
	if err == nil {
		return errors.New("the port-forward ended")
	}
	return fmt.Errorf("port-forward to %s failed: %w (is `create pods/portforward` allowed in %s?)", t, err, t.Namespace)
}
