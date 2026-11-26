package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/joao00001/pg-regression-radar/internal/dashboard/layout"
)

// Profile is what one person keeps of the dashboard between runs: the edits made
// to the layout (panel settings, queries, positions) and the time range of each
// board. It is one small JSON file in the command's own data directory, written
// only by the page's own save requests and never anywhere else.
//
// The dashboard stays read-only toward the cluster and the radar. This file is
// the only thing it ever writes, and its path is fixed: no request chooses it.
type Profile struct {
	dir, file string
	mu        sync.Mutex
}

// MaxProfile is far more than a profile needs (a few KiB).
const MaxProfile = 256 << 10

// profileVersion is the format of the file; a different one is ignored, not guessed at.
const profileVersion = 1

// DefaultDataDir is where the dashboard keeps its profile unless told otherwise:
// the person's own configuration directory for this tool.
func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no per-user configuration directory here (%w); pass --data-dir", err)
	}
	return filepath.Join(base, "pg-regression-radar", "dashboard"), nil
}

// ProfileKey names the profile of one dashboard source: "default" for the built-in
// boards, or a short digest of the absolute path of a --config file. Two dashboard
// files do not share edits, and moving a file is a new dashboard.
func ProfileKey(configPath string) string {
	if configPath == "" {
		return "default"
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	sum := sha256.Sum256([]byte(abs))
	return "file-" + hex.EncodeToString(sum[:])[:10]
}

// OpenProfile prepares dir (private to the person) and proves it can be written, so
// a read-only volume is reported at start and not at the first edit.
func OpenProfile(dir, key string) (*Profile, error) {
	if dir == "" {
		return nil, errors.New("no data directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return nil, err
	}
	name := probe.Name()
	_ = probe.Close()
	if err := os.Remove(name); err != nil {
		return nil, err
	}
	return &Profile{dir: dir, file: filepath.Join(dir, "dashboard-"+key+".json")}, nil
}

// Where is the file the profile is kept in.
func (p *Profile) Where() string { return p.file }

// Read returns the saved profile, or nil when there is none yet.
func (p *Profile) Read() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := os.Open(p.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxProfile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxProfile {
		return nil, fmt.Errorf("%s is larger than %d KiB", p.file, MaxProfile>>10)
	}
	return data, nil
}

// Write replaces the profile as a whole, atomically: a crash leaves the old file
// or the new one, never half of one.
func (p *Profile) Write(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	tmp, err := os.CreateTemp(p.dir, ".profile-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, p.file); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// Delete forgets the profile; no profile is not an error.
func (p *Profile) Delete() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.Remove(p.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ValidateProfile accepts only a profile this version wrote: a JSON object of
// known fields, a layout the dashboard's own validator accepts, and ranges that
// are plainly ranges. It is checked on the way in, so the file never holds
// something the page could not use.
func ValidateProfile(data []byte) error {
	if len(data) > MaxProfile {
		return fmt.Errorf("a profile is a few KiB; this one is %d KiB", len(data)>>10)
	}
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&top); err != nil {
		return fmt.Errorf("not a JSON object: %v", err)
	}
	if dec.More() {
		return errors.New("more than one JSON value")
	}
	for k := range top {
		switch k {
		case "v", "base", "layout", "ranges":
		default:
			return fmt.Errorf("unknown field %q", k)
		}
	}
	var v int
	if err := json.Unmarshal(top["v"], &v); err != nil || v != profileVersion {
		return fmt.Errorf("v must be %d", profileVersion)
	}
	if raw, ok := top["base"]; ok {
		var base string
		if err := json.Unmarshal(raw, &base); err != nil || len(base) > 256 {
			return errors.New("base must be a short string")
		}
	}
	if raw, ok := top["layout"]; ok {
		if _, ps := layout.Parse(raw); len(ps) > 0 {
			return fmt.Errorf("layout: %s", ps[0].Msg)
		}
	}
	if raw, ok := top["ranges"]; ok {
		var ranges map[string]map[string]any
		if err := json.Unmarshal(raw, &ranges); err != nil {
			return errors.New("ranges must map a board id to a range")
		}
		if len(ranges) > 64 {
			return errors.New("too many ranges")
		}
		for id, r := range ranges {
			if len(id) == 0 || len(id) > 64 {
				return errors.New("a range's board id is 1 to 64 characters")
			}
			if k := r["kind"]; k != "rel" && k != "abs" {
				return fmt.Errorf("the range of %q is neither relative nor absolute", id)
			}
		}
	}
	return nil
}
