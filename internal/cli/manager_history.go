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

package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/observe"
	"github.com/joao00001/pg-regression-radar/internal/storage"
	"github.com/joao00001/pg-regression-radar/internal/storage/postgres"
)

// historyConfig is what the manager's --observe-history* flags ask for.
type historyConfig struct {
	Kind       string        // "memory" (default) or "postgres"
	DSNFile    string        // file holding the Postgres DSN; never the DSN itself
	Retention  time.Duration // how long a deploy is kept
	MaxDeploys int           // memory only: the ring's size
}

// validate rejects a configuration that could not work, before anything starts.
func (c historyConfig) validate() error {
	switch c.Kind {
	case "memory", "":
		if c.DSNFile != "" {
			return errors.New("--observe-history-dsn-file is only used with --observe-history=postgres")
		}
	case "postgres":
		if c.DSNFile == "" {
			return errors.New("--observe-history=postgres needs --observe-history-dsn-file (a file, such as a mounted Secret, that holds the DSN: a DSN on the command line would show in the pod spec)")
		}
	default:
		return fmt.Errorf("--observe-history=%q: want memory or postgres", c.Kind)
	}
	if c.Retention < 0 {
		return errors.New("--observe-history-retention cannot be negative")
	}
	if c.MaxDeploys < 0 {
		return errors.New("--observe-history-max-deploys cannot be negative")
	}
	return nil
}

// readDSN reads the DSN from its file. Only the problem is reported, never the
// content, because the text is a credential.
func readDSN(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names this file in the pod spec
	if err != nil {
		return "", fmt.Errorf("--observe-history-dsn-file: %w", errors.Unwrap(err))
	}
	dsn := strings.TrimSpace(string(b))
	if dsn == "" {
		return "", fmt.Errorf("--observe-history-dsn-file %s is empty", path)
	}
	return dsn, nil
}

// openHistory builds the store the flags describe. The returned stop function
// releases it; prune runs the retention sweep until ctx ends (nil for memory,
// which applies its own bound on every write).
func openHistory(ctx context.Context, c historyConfig, log *slog.Logger) (store observe.HistoryStore, prune func(context.Context), stop func(), err error) {
	if err := c.validate(); err != nil {
		return nil, nil, nil, err
	}
	ret := c.Retention
	if ret == 0 {
		ret = 30 * 24 * time.Hour
	}
	if c.Kind == "postgres" {
		dsn, err := readDSN(c.DSNFile)
		if err != nil {
			return nil, nil, nil, err
		}
		octx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		h, err := postgres.OpenHistory(octx, dsn)
		if err != nil {
			return nil, nil, nil, err
		}
		return h, func(ctx context.Context) {
			storage.RunPruneLoop(ctx, "observe-history", h, 15*time.Minute, ret, log)
		}, func() { _ = h.Close() }, nil
	}
	return &observe.MemoryHistory{MaxDeploys: c.MaxDeploys, Retention: ret}, nil, func() {}, nil
}
