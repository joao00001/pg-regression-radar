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

package pglog

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// maxLine bounds one log line. A statement longer than this (a giant IN list)
// is skipped: it would not be one of the sampled short queries a latency series
// is made of, and reading it whole would be a way to exhaust memory.
const maxLine = 1 << 20

// Consume reads jsonlog lines from r until it ends or ctx is done and hands
// every statement duration it finds to sink. It returns the number of lines
// skipped for being too long, and r's error if it ended on one (io.EOF is not an
// error: the log simply ended).
func Consume(ctx context.Context, r io.Reader, sink func(Record)) (skipped int, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	tooLong := false
	for {
		if ctx.Err() != nil {
			return skipped, ctx.Err()
		}
		chunk, isPrefix, rerr := br.ReadLine()
		if len(chunk) > 0 && !tooLong {
			if len(line)+len(chunk) > maxLine {
				tooLong = true
				line = line[:0]
			} else {
				line = append(line, chunk...)
			}
		}
		if rerr == nil && isPrefix {
			continue
		}
		if tooLong {
			skipped++
		} else if len(line) > 0 {
			if rec, ok := ParseLine(line, time.Now()); ok {
				sink(rec)
			}
		}
		line, tooLong = line[:0], false
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return skipped, nil
			}
			return skipped, rerr
		}
	}
}

// FollowFile reads the file at path from its current end, like tail -F: it waits
// for new lines, notices that the file was truncated or replaced (rotation) and
// starts over from the top of the new one. It returns when ctx is done.
//
// It is for a Postgres whose log directory the manager can read (a mounted
// volume, a sidecar, a test); on Kubernetes use FollowPods.
func FollowFile(ctx context.Context, path string, poll time.Duration, sink func(Record)) error {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	var (
		f    *os.File
		info os.FileInfo
		off  int64
		pend []byte
	)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	first := true
	for {
		if f == nil {
			nf, err := os.Open(path)
			if err == nil {
				f = nf
				info, _ = f.Stat()
				if first {
					off, _ = f.Seek(0, io.SeekEnd)
				} else {
					off = 0
				}
				first = false
			}
		}
		if f != nil {
			cur, err := os.Stat(path)
			switch {
			case err != nil:
				// removed: wait for it to come back
				_ = f.Close()
				f, pend = nil, nil
			case !os.SameFile(info, cur) || cur.Size() < off:
				_ = f.Close()
				f, pend = nil, nil
				continue
			case cur.Size() > off:
				buf := make([]byte, cur.Size()-off)
				n, _ := f.ReadAt(buf, off)
				off += int64(n)
				pend = append(pend, buf[:n]...)
				for {
					i := bytes.IndexByte(pend, '\n')
					if i < 0 {
						break
					}
					if rec, ok := ParseLine(pend[:i], time.Now()); ok {
						sink(rec)
					}
					pend = pend[i+1:]
				}
				if len(pend) > maxLine {
					pend = nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}
