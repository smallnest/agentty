package terminal

import "sync"

// Ring is a bounded byte buffer holding recent PTY output so a client that
// attaches (or reconnects) can rebuild its screen by replaying raw bytes.
//
// Replay is a pragmatic substitute for a real VT snapshot: full-screen TUIs
// redraw on resize, so feeding the tail of the stream back to xterm.js usually
// reconstructs the visible screen.
type Ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// NewRing creates a ring bounded to max bytes.
func NewRing(max int) *Ring {
	if max <= 0 {
		max = 256 * 1024
	}
	return &Ring{max: max}
}

// Write appends p, trimming the oldest bytes past the bound.
func (r *Ring) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) >= r.max {
		// A single chunk larger than the whole buffer: keep only its tail.
		r.buf = append(r.buf[:0], p[len(p)-r.max:]...)
		r.trimStartLocked()
		return
	}
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		drop := len(r.buf) - r.max
		copy(r.buf, r.buf[drop:])
		r.buf = r.buf[:r.max]
		r.trimStartLocked()
	}
}

// trimStartLocked advances to the next escape byte when the buffer starts
// mid-sequence, so replay does not begin with a broken CSI.
func (r *Ring) trimStartLocked() {
	limit := 64
	if len(r.buf) < limit {
		limit = len(r.buf)
	}
	for i := 0; i < limit; i++ {
		if r.buf[i] == 0x1b {
			if i > 0 {
				copy(r.buf, r.buf[i:])
				r.buf = r.buf[:len(r.buf)-i]
			}
			return
		}
	}
}

// Snapshot returns a copy of the current buffer.
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// Len reports the buffered byte count.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}
