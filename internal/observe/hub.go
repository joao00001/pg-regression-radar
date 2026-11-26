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

package observe

import (
	"sync"
	"time"
)

// Hub fans events out to subscribers. Publishing never blocks: a subscriber
// whose buffer is full misses events, because the radar must never wait for a
// browser. A missed event is repaired by the client, which refetches the
// snapshot when it sees a gap in Seq or reconnects.
type Hub struct {
	mu   sync.Mutex
	seq  uint64
	subs map[chan Event]struct{}
	now  func() time.Time
	buf  int
}

// NewHub returns a hub whose subscribers buffer buf events (64 if buf <= 0).
func NewHub(buf int) *Hub {
	if buf <= 0 {
		buf = 64
	}
	return &Hub{subs: map[chan Event]struct{}{}, now: time.Now, buf: buf}
}

// Publish stamps e with the next sequence number and the time, and offers it to
// every subscriber.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	e.Seq = h.seq
	if e.At.IsZero() {
		e.At = h.now()
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe returns a channel of future events and a cancel function.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, h.buf)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}
