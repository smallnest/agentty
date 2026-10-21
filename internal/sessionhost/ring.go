package sessionhost

import "sync"

// ring is a bounded replay buffer of PTY output.
type ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRing(max int) *ring {
	if max <= 0 {
		max = 256 * 1024
	}
	return &ring{max: max}
}

func (r *ring) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(data) >= r.max {
		r.buf = append(r.buf[:0], data[len(data)-r.max:]...)
		r.trimStartLocked()
		return
	}
	r.buf = append(r.buf, data...)
	if len(r.buf) > r.max {
		drop := len(r.buf) - r.max
		copy(r.buf, r.buf[drop:])
		r.buf = r.buf[:r.max]
		r.trimStartLocked()
	}
}

func (r *ring) trimStartLocked() {
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

func (r *ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}
