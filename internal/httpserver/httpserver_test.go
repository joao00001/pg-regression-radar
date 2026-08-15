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

package httpserver_test

import (
	"net/http"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/httpserver"
)

func TestNew_ReturnsConfiguredServer(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	srv := httpserver.New(":0", mux)

	if srv == nil {
		t.Fatal("New returned nil")
	}
	if srv.Addr != ":0" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":0")
	}
	if srv.Handler != mux {
		t.Error("Handler was not set to the provided mux")
	}
}

func TestNew_TimeoutsAreSet(t *testing.T) {
	t.Parallel()

	srv := httpserver.New(":0", http.NewServeMux())

	if srv.ReadHeaderTimeout != httpserver.DefaultReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, httpserver.DefaultReadHeaderTimeout)
	}
	if srv.ReadTimeout != httpserver.DefaultReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, httpserver.DefaultReadTimeout)
	}
	if srv.WriteTimeout != httpserver.DefaultWriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, httpserver.DefaultWriteTimeout)
	}
	if srv.IdleTimeout != httpserver.DefaultIdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, httpserver.DefaultIdleTimeout)
	}
}
