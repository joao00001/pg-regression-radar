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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryFlagsAreChecked(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]historyConfig{
		"unknown kind":       {Kind: "sqlite"},
		"postgres, no file":  {Kind: "postgres"},
		"memory with a file": {Kind: "memory", DSNFile: empty},
		"negative retention": {Kind: "memory", Retention: -time.Hour},
		"negative size":      {Kind: "memory", MaxDeploys: -1},
	} {
		if _, _, _, err := openHistory(context.Background(), c, nil); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	if _, _, _, err := openHistory(context.Background(), historyConfig{Kind: "postgres", DSNFile: filepath.Join(dir, "missing")}, nil); err == nil {
		t.Error("a missing DSN file should be refused")
	}
	if _, _, _, err := openHistory(context.Background(), historyConfig{Kind: "postgres", DSNFile: empty}, nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty DSN file should be refused: %v", err)
	}
}

func TestHistoryDefaultsToMemoryWithoutPruning(t *testing.T) {
	store, prune, stop, err := openHistory(context.Background(), historyConfig{Kind: "memory"}, nil)
	if err != nil || store == nil || prune != nil {
		t.Fatalf("memory: %v %v %v", store, prune != nil, err)
	}
	stop()
}

func TestADSNFileErrorNeverQuotesTheDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsn")
	secret := "postgres://u:hunter2@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _, _, err := openHistory(ctx, historyConfig{Kind: "postgres", DSNFile: path}, nil)
	if err == nil {
		t.Fatal("nothing listens there")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error carries the credential: %v", err)
	}
}
