// SPDX-License-Identifier: AGPL-3.0-or-later
package download

import (
	"io"
	"sync"
	"time"
)

// Progress is transient: Received counts network traffic, while Downloaded
// includes verified resume data and current response buffers. Only the existing
// committed-chunk callback may update durable recovery state.
type Progress struct {
	Downloaded  int64
	Received    int64
	Connections int
	Phase       string
}

type transferTracker struct {
	mu        sync.Mutex
	state     Progress
	total     int64
	committed int64
	parts     map[int]int64
	last      time.Time
	observe   func(Progress)
}

func newTransferTracker(total, done int64, observe func(Progress)) *transferTracker {
	t := &transferTracker{total: total, committed: done, parts: map[int]int64{}, observe: observe, state: Progress{Phase: "downloading"}}
	t.emit(true)
	return t
}

// All mutations and callbacks are serialized, including parallel Range readers.
func (t *transferTracker) emit(force bool) {
	if t.observe == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(t.last) < 200*time.Millisecond {
		return
	}
	t.last = now
	t.state.Downloaded = t.committed
	for _, n := range t.parts {
		t.state.Downloaded += n
	}
	t.state.Downloaded = min(t.total, t.state.Downloaded)
	t.observe(t.state)
}
func (t *transferTracker) begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Connections++
	t.emit(true)
}
func (t *transferTracker) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Connections--
	t.emit(true)
}
func (t *transferTracker) read(part int, n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	first := t.parts[part] == 0
	t.parts[part] += int64(n)
	t.state.Received += int64(n)
	t.emit(first)
}
func (t *transferTracker) commit(part int, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.parts, part)
	t.committed += n
	t.emit(true)
}
func (t *transferTracker) discard(part int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.parts, part)
	t.emit(true)
}
func (t *transferTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.committed = 0
	t.parts = map[int]int64{}
	t.emit(true)
}
func (t *transferTracker) phase(phase string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Phase = phase
	t.emit(true)
}

type progressReader struct {
	reader  io.Reader
	tracker *transferTracker
	part    int
}

func (r progressReader) Read(buf []byte) (int, error) {
	n, err := r.reader.Read(buf)
	if n > 0 {
		r.tracker.read(r.part, n)
	}
	return n, err
}
