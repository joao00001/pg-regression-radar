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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDashboardRefusesWhatItCannotDo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no kubeconfig", []string{"--kubeconfig", "/no/such/kubeconfig"}, 1, "kubeconfig"},
		{"bad port", []string{"--fixture", "x.json", "--port", "0"}, 2, "not a valid port"},
		{"bad port high", []string{"--fixture", "x.json", "--port", "70000"}, 2, "not a valid port"},
		{"bad remote port", []string{"--remote-port", "0"}, 2, "--remote-port"},
		{"missing file", []string{"--fixture", "/no/such/fixture.json"}, 1, "no such file"},
		{"unknown flag", []string{"--nope"}, 2, ""},
		{"fixture and url", []string{"--fixture", "x.json", "--url", "http://127.0.0.1:1"}, 2, "cannot be used together"},
		{"url is not http", []string{"--url", "ftp://x"}, 1, "not an http(s) URL"},
		{"url without host", []string{"--url", "http://"}, 1, "not an http(s) URL"},
		{"listen on all interfaces", []string{"--fixture", "x.json", "--listen", "0.0.0.0"}, 2, "--insecure-listen"},
		{"listen on a lan address", []string{"--fixture", "x.json", "--listen", "192.168.1.20"}, 2, "no sign-in"},
		{"listen on a name", []string{"--fixture", "x.json", "--listen", "example.com"}, 2, "not an IP address"},
	} {
		var out, errb bytes.Buffer
		if got := runDashboard(context.Background(), tc.args, &out, &errb); got != tc.code {
			t.Errorf("%s: exit %d, want %d (%s)", tc.name, got, tc.code, errb.String())
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%s: stderr %q lacks %q", tc.name, errb.String(), tc.want)
		}
	}
}

func TestCheckListen(t *testing.T) {
	for _, tc := range []struct {
		host     string
		insecure bool
		ok       bool
	}{
		{"127.0.0.1", false, true},
		{"127.0.0.2", false, true},
		{"::1", false, true},
		{"localhost", false, true},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"10.0.0.5", false, false},
		{"10.0.0.5", true, true},
		{"0.0.0.0", true, true},
	} {
		if err := checkListen(tc.host, tc.insecure); (err == nil) != tc.ok {
			t.Errorf("checkListen(%q, %v) = %v, want ok=%v", tc.host, tc.insecure, err, tc.ok)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// runBackground runs the command until the returned stop is called.
func runBackground(t *testing.T, args []string) (stop func() (int, string)) {
	t.Helper()
	// Never the real configuration directory of whoever runs the tests.
	hasDir := false
	for _, a := range args {
		if strings.HasPrefix(a, "--data-dir") || a == "--no-save" {
			hasDir = true
		}
	}
	if !hasDir {
		args = append([]string{"--data-dir", t.TempDir()}, args...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out, errb safeBuf
	done := make(chan int, 1)
	go func() { done <- runDashboard(ctx, args, &out, &errb) }()
	return func() (int, string) {
		cancel()
		select {
		case c := <-done:
			return c, out.String() + errb.String()
		case <-time.After(5 * time.Second):
			t.Fatal("dashboard did not stop")
			return 0, ""
		}
	}
}

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	var last error
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err != nil {
			last = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	t.Fatalf("GET %s: %v", url, last)
	return 0, ""
}

func TestDashboardReadsAManagerAtAURL(t *testing.T) {
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	defer manager.Close()

	port := freePort(t)
	stop := runBackground(t, []string{"--url", manager.URL, "--port", strconv.Itoa(port)})
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	code, body := getBody(t, base+"/observe/v1/overview")
	if code != 200 || !strings.Contains(body, "/observe/v1/overview") {
		t.Fatalf("proxy: %d %s", code, body)
	}
	code, body = getBody(t, base+"/dashboard/config")
	var cfg struct{ Origin, Note string }
	if err := json.Unmarshal([]byte(body), &cfg); err != nil || code != 200 || cfg.Origin != "cluster" {
		t.Fatalf("config: %d %s %v", code, body, err)
	}
	code, body = getBody(t, base+"/dashboard/status")
	if code != 200 || !strings.Contains(body, `"connected"`) {
		t.Fatalf("status: %d %s", code, body)
	}
	if exit, out := stop(); exit != 0 || !strings.Contains(out, "Dashboard on http://127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("exit %d, output %q", exit, out)
	}
}

func TestDashboardRejectsAForeignHostAndMethod(t *testing.T) {
	manager := httptest.NewServer(http.NotFoundHandler())
	defer manager.Close()
	port := freePort(t)
	stop := runBackground(t, []string{"--url", manager.URL, "--port", strconv.Itoa(port)})
	defer stop()
	getBody(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/status") // wait until it is up

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/status", nil)
	req.Host = "evil.example:" + strconv.Itoa(port)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host got %d", resp.StatusCode)
	}
	resp, err = http.Post("http://127.0.0.1:"+strconv.Itoa(port)+"/observe/v1/overview", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST got %d", resp.StatusCode)
	}
}

func TestDashboardInsecureListenWarns(t *testing.T) {
	manager := httptest.NewServer(http.NotFoundHandler())
	defer manager.Close()
	port := freePort(t)
	stop := runBackground(t, []string{"--url", manager.URL, "--port", strconv.Itoa(port), "--listen", "127.0.0.1", "--insecure-listen"})
	getBody(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/status")
	// It does not write to disk for anyone who can reach it, even with --data-dir given.
	_, prof := getBody(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/profile")
	if !strings.Contains(prof, `"saved":false`) {
		t.Fatalf("an exposed dashboard must not save: %s", prof)
	}
	req0, _ := http.NewRequest(http.MethodPut, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/profile", strings.NewReader(`{"v":1}`))
	req0.Header.Set("Content-Type", "application/json")
	req0.Header.Set("X-Requested-By", "pgrr-dashboard")
	if r0, err := http.DefaultClient.Do(req0); err != nil || r0.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT on an exposed dashboard: %v %v", r0, err)
	}
	// With --insecure-listen any Host name is served on that port.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/status", nil)
	req.Host = "dash.internal:" + strconv.Itoa(port)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	_, out := stop()
	if !strings.Contains(out, "WARNING") {
		t.Fatalf("no warning in %q", out)
	}
	if !strings.Contains(out, "saving is off with --insecure-listen") {
		t.Fatalf("it should say why nothing is saved: %q", out)
	}
}

func TestDashboardConfigValidateAndDump(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runDashboard(context.Background(), []string{"config", "dump"}, &out, &errb); code != 0 {
		t.Fatalf("dump: %d %s", code, errb.String())
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runDashboard(context.Background(), []string{"config", "validate", good}, &out, &errb); code != 0 || !strings.Contains(out.String(), "ok") {
		t.Fatalf("what dump prints must validate: %d %s %s", code, out.String(), errb.String())
	}

	bad := filepath.Join(dir, "bad.yaml")
	src := strings.Replace(string(mustRead(t, good)), "type: summary", "type: sumary", 1)
	if err := os.WriteFile(bad, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := runDashboard(context.Background(), []string{"config", "validate", bad}, &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), bad+":") || !strings.Contains(errb.String(), `did you mean "summary"`) {
		t.Fatalf("stderr %q", errb.String())
	}

	errb.Reset()
	if code := runDashboard(context.Background(), []string{"config", "validate", "/no/such/file.yaml"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no such file") {
		t.Fatalf("%d %s", code, errb.String())
	}
	for _, args := range [][]string{{"config"}, {"config", "validate"}, {"config", "nope"}} {
		errb.Reset()
		if code := runDashboard(context.Background(), args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	out.Reset()
	if code := runDashboard(context.Background(), []string{"config", "dump", "--catalog"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "ratio-chart") || !strings.Contains(out.String(), "option limit") {
		t.Fatalf("catalog: %d %s", code, out.String())
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDashboardStartsWithABadConfigOnlyToSayWhy(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "d.yaml")
	if err := os.WriteFile(bad, []byte("kind: Dashboard\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := runDashboard(context.Background(), []string{"--fixture", "../observe/testdata/recorded-sim.json", "--config", bad, "--port", strconv.Itoa(freePort(t))}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "apiVersion is missing") || strings.Contains(out.String(), "Dashboard on") {
		t.Fatalf("%d out=%q err=%q", code, out.String(), errb.String())
	}
}

func TestDashboardServesItsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.yaml")
	src := "apiVersion: pgrr.io/v1\nkind: Dashboard\nboards:\n  - id: mine\n    title: Mine\n    panels:\n      - {id: s, type: summary, at: [0, 0, 12, 3]}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	stop := runBackground(t, []string{"--fixture", "../observe/testdata/recorded-sim.json", "--config", path, "--port", strconv.Itoa(port)})
	code, body := getBody(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/layout")
	stop()
	if code != 200 || !strings.Contains(body, `"id":"mine"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestDashboardSavesEditsInItsDataDirectory(t *testing.T) {
	manager := httptest.NewServer(http.NotFoundHandler())
	defer manager.Close()
	dir := filepath.Join(t.TempDir(), "data")
	port := freePort(t)
	stop := runBackground(t, []string{"--url", manager.URL, "--port", strconv.Itoa(port), "--data-dir", dir})
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	_, body := getBody(t, base+"/dashboard/profile")
	var got struct {
		Saved bool
		Where string
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || !got.Saved || filepath.Dir(got.Where) != dir {
		t.Fatalf("profile: %s %v", body, err)
	}
	exit, out := stop()
	if exit != 0 || !strings.Contains(out, "Your edits are saved in "+got.Where) {
		t.Fatalf("the command should say where edits go: exit %d, %q", exit, out)
	}
}

func TestDashboardNoSaveAndUnusableDataDirectoryKeepEditsInTheBrowser(t *testing.T) {
	manager := httptest.NewServer(http.NotFoundHandler())
	defer manager.Close()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"--no-save":       {"--no-save"},
		"a bad directory": {"--data-dir", filepath.Join(file, "sub")},
	} {
		port := freePort(t)
		stop := runBackground(t, append([]string{"--url", manager.URL, "--port", strconv.Itoa(port)}, args...))
		_, body := getBody(t, "http://127.0.0.1:"+strconv.Itoa(port)+"/dashboard/profile")
		if !strings.Contains(body, `"saved":false`) {
			t.Errorf("%s: %s", name, body)
		}
		exit, out := stop()
		if exit != 0 || strings.Contains(out, "Your edits are saved") {
			t.Errorf("%s: exit %d, %q", name, exit, out)
		}
		if name == "a bad directory" && !strings.Contains(out, "kept in the browser only") {
			t.Errorf("%s: should say why edits are not saved: %q", name, out)
		}
	}
}
