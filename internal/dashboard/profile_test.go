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

package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/dashboard/layout"
)

// goodProfile is a profile the page could have written: the default layout with
// one panel's query set, and a range.
func goodProfile(t *testing.T) []byte {
	t.Helper()
	d := layout.Default()
	d.Boards[0].Panels[0].Query = "app:checkout"
	lj, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"v": 1, "base": "abc", "layout": json.RawMessage(lj),
		"ranges": map[string]any{"now": map[string]any{"kind": "rel", "id": "6h"}},
	})
	return b
}

func profileServer(t *testing.T) (*httptest.Server, *Profile) {
	t.Helper()
	p, err := OpenProfile(filepath.Join(t.TempDir(), "data"), ProfileKey(""))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: http.NotFoundHandler(), Profile: p}
	ts := httptest.NewServer(s.Handler())
	s.Port = strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	t.Cleanup(ts.Close)
	return ts, p
}

func send(t *testing.T, method, url string, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

var okHdr = map[string]string{"Content-Type": "application/json", writeHeader: writeHeaderValue}

func TestProfileSavesAndComesBack(t *testing.T) {
	ts, p := profileServer(t)
	url := ts.URL + "/dashboard/profile"

	var got struct {
		Saved   bool            `json:"saved"`
		Where   string          `json:"where"`
		Profile json.RawMessage `json:"profile"`
	}
	read := func() {
		got.Profile = nil
		r := send(t, "GET", url, nil, nil)
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if !got.Saved || got.Where != p.Where() || string(got.Profile) != "null" {
		t.Fatalf("a new profile is empty but saved: %+v", got)
	}

	want := goodProfile(t)
	if r := send(t, "PUT", url, want, okHdr); r.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("PUT = %d %s", r.StatusCode, b)
	}
	read()
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(got.Profile, &b)
	if !jsonEqual(a, b) {
		t.Fatalf("what was saved is what comes back:\n%s\n%s", want, got.Profile)
	}
	// It is on disk, for the next run, private to the person.
	if _, err := os.Stat(p.Where()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p.Where())
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("profile mode = %v, want 0600", st.Mode().Perm())
		}
		dir, _ := os.Stat(filepath.Dir(p.Where()))
		if dir.Mode().Perm() != 0o700 {
			t.Fatalf("data dir mode = %v, want 0700", dir.Mode().Perm())
		}
	}
	// A second run opens the same file.
	p2, err := OpenProfile(filepath.Dir(p.Where()), ProfileKey(""))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := p2.Read()
	if string(data) != string(want) {
		t.Fatal("a new run did not find the saved profile")
	}
	// DELETE forgets it, and forgetting twice is fine.
	for i := 0; i < 2; i++ {
		if r := send(t, "DELETE", url, nil, okHdr); r.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE = %d", r.StatusCode)
		}
	}
	read()
	if string(got.Profile) != "null" {
		t.Fatal("deleted profile still comes back")
	}
	// No temporary files are left behind.
	ents, _ := os.ReadDir(filepath.Dir(p.Where()))
	if len(ents) != 0 {
		t.Fatalf("data dir not clean: %v", ents)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestProfileWritesNeedToComeFromThePage(t *testing.T) {
	ts, p := profileServer(t)
	url := ts.URL + "/dashboard/profile"
	body := goodProfile(t)
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no marker header", map[string]string{"Content-Type": "application/json"}, http.StatusForbidden},
		{"another site's origin", map[string]string{"Content-Type": "application/json", writeHeader: writeHeaderValue, "Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site fetch", map[string]string{"Content-Type": "application/json", writeHeader: writeHeaderValue, "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"a form post type", map[string]string{"Content-Type": "text/plain", writeHeader: writeHeaderValue}, http.StatusUnsupportedMediaType},
		{"the page itself", map[string]string{"Content-Type": "application/json", writeHeader: writeHeaderValue, "Origin": ts.URL, "Sec-Fetch-Site": "same-origin"}, http.StatusNoContent},
	}
	for _, c := range cases {
		if r := send(t, "PUT", url, body, c.hdr); r.StatusCode != c.want {
			t.Errorf("%s: PUT = %d, want %d", c.name, r.StatusCode, c.want)
		}
	}
	// A refused write changes nothing; only the last case wrote.
	if r := send(t, "DELETE", url, nil, map[string]string{}); r.StatusCode != http.StatusForbidden {
		t.Errorf("DELETE without the marker = %d", r.StatusCode)
	}
	if data, _ := p.Read(); data == nil {
		t.Error("a refused delete removed the profile")
	}
	// A Host that is not this machine is refused as it is for reads.
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
	req.Host = "rebind.example:" + strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	for k, v := range okHdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("rebound Host: PUT = %d", resp.StatusCode)
	}
	// Nothing else can be written.
	for _, path := range []string{"/dashboard/layout", "/dashboard/config", "/observe/v1/actions", "/"} {
		if r := send(t, "PUT", ts.URL+path, body, okHdr); r.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("PUT %s = %d, want 405", path, r.StatusCode)
		}
	}
	if r := send(t, "POST", url, body, okHdr); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", r.StatusCode)
	}
}

func TestProfileRefusesWhatThePageCouldNotUse(t *testing.T) {
	ts, p := profileServer(t)
	url := ts.URL + "/dashboard/profile"
	good := goodProfile(t)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(good, &m)
	with := func(k string, v string) []byte {
		c := map[string]json.RawMessage{}
		for a, b := range m {
			c[a] = b
		}
		c[k] = json.RawMessage(v)
		out, _ := json.Marshal(c)
		return out
	}
	for name, body := range map[string][]byte{
		"not JSON":                    []byte("boards: []"),
		"an array":                    []byte("[]"),
		"two values":                  append(append([]byte{}, good...), good...),
		"an unknown key":              with("path", `"/etc/passwd"`),
		"another version":             with("v", `2`),
		"an invalid layout":           with("layout", `{"apiVersion":"pgrr.io/v1","kind":"Dashboard","boards":[{"id":"a","title":"A","range":"6h","panels":[{"id":"p","type":"nonsense","at":[0,0,4,4]}]}]}`),
		"a layout of the wrong shape": with("layout", `"x"`),
		"a range that is neither":     with("ranges", `{"now":{"kind":"sometime"}}`),
		"a range that is a string":    with("ranges", `{"now":"6h"}`),
	} {
		if r := send(t, "PUT", url, body, okHdr); r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: PUT = %d, want 422", name, r.StatusCode)
		}
	}
	big := bytes.Repeat([]byte(" "), MaxProfile+10)
	if r := send(t, "PUT", url, big, okHdr); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize PUT = %d, want 413", r.StatusCode)
	}
	if data, _ := p.Read(); data != nil {
		t.Error("a refused profile was written")
	}
}

func TestAProfileOnDiskThatCannotBeUsedIsReportedNotServed(t *testing.T) {
	ts, p := profileServer(t)
	if err := p.Write([]byte(`{"v":9}`)); err != nil {
		t.Fatal(err)
	}
	r := send(t, "GET", ts.URL+"/dashboard/profile", nil, nil)
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if got["profile"] != nil || got["problem"] == nil {
		t.Fatalf("a profile of another version is reported and not served: %v", got)
	}
	// Saving replaces it.
	if r := send(t, "PUT", ts.URL+"/dashboard/profile", goodProfile(t), okHdr); r.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", r.StatusCode)
	}
}

func TestWithoutADataDirectoryNothingIsSaved(t *testing.T) {
	s := &Server{Backend: http.NotFoundHandler()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.Port = strings.TrimPrefix(ts.URL[strings.LastIndex(ts.URL, ":"):], ":")
	r := send(t, "GET", ts.URL+"/dashboard/profile", nil, nil)
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if got["saved"] != false {
		t.Fatalf("GET = %v", got)
	}
	if r := send(t, "PUT", ts.URL+"/dashboard/profile", goodProfile(t), okHdr); r.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT = %d, want 404", r.StatusCode)
	}
}

func TestProfileKeysAndDirectories(t *testing.T) {
	if ProfileKey("") != "default" {
		t.Fatal("the built-in boards have one profile")
	}
	a, b := ProfileKey("a.yaml"), ProfileKey("b.yaml")
	if a == b || !strings.HasPrefix(a, "file-") || a != ProfileKey("a.yaml") {
		t.Fatalf("each dashboard file has its own, stable, profile: %s %s", a, b)
	}
	// A file where the directory should be is reported when starting.
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o600)
	if _, err := OpenProfile(filepath.Join(f, "sub"), "default"); err == nil {
		t.Fatal("an unusable data directory must be an error")
	}
	if _, err := OpenProfile("", "default"); err == nil {
		t.Fatal("an empty data directory must be an error")
	}
}
