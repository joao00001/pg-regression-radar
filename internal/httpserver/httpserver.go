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

// Package httpserver provides a shared constructor for all HTTP servers in
// pg-regression-radar. Every server created here uses identical, conservative
// timeout defaults so that no mode is left vulnerable to slow-client /
// Slowloris-style resource exhaustion via connection hold.
package httpserver

import (
	"net/http"
	"time"
)

// Default timeout values applied to every server created by New.
// They are intentionally conservative:
//   - ReadHeaderTimeout: prevents a client that connects but never sends
//     request headers from holding the goroutine indefinitely.
//   - ReadTimeout: caps the total time allowed to read the full request,
//     including the body.
//   - WriteTimeout: caps the time allowed to write the response.
//   - IdleTimeout: closes keep-alive connections that have been idle for
//     longer than this duration.
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
)

// New returns an *http.Server bound to addr with handler as its root handler
// and all four timeout fields set to the project-wide defaults above.
// Callers must still call srv.ListenAndServe() (or srv.Serve()) and handle
// graceful shutdown themselves.
func New(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		ReadTimeout:       DefaultReadTimeout,
		WriteTimeout:      DefaultWriteTimeout,
		IdleTimeout:       DefaultIdleTimeout,
	}
}
