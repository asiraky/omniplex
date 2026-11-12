package server

// termRing keeps the tail of a terminal's output so a socket that comes back
// after a drop can be caught up on what it missed. Offsets are positions in
// the whole stream since the pty started, not in the buffer, so a client can
// say "I have everything before N" and mean the same byte on every socket.
//
// Not safe for concurrent use; the session's lock guards it.
type termRing struct {
	buf   []byte
	total int64 // bytes ever written; the offset of the next one
}

func newTermRing(size int) *termRing {
	return &termRing{buf: make([]byte, size)}
}

// Write appends p, overwriting the oldest bytes once the buffer is full.
func (r *termRing) Write(p []byte) {
	size := len(r.buf)
	r.total += int64(len(p))
	// Only the last size bytes of a large write can survive it.
	if len(p) > size {
		p = p[len(p)-size:]
	}
	at := int((r.total - int64(len(p))) % int64(size))
	n := copy(r.buf[at:], p)
	copy(r.buf, p[n:])
}

// Start is the offset of the oldest byte still held.
func (r *termRing) Start() int64 {
	return max(0, r.total-int64(len(r.buf)))
}

// Total is the offset of the next byte to be written.
func (r *termRing) Total() int64 {
	return r.total
}

// Since returns up to limit bytes from offset off, copied out, and the offset
// they actually start at. An offset the buffer no longer holds, or one that
// was never written, is answered from Start: the caller sees the jump in the
// returned offset and can tell its client.
func (r *termRing) Since(off int64, limit int) ([]byte, int64) {
	if off < r.Start() || off > r.total {
		off = r.Start()
	}
	n := int(min(r.total-off, int64(limit)))
	out := make([]byte, n)
	size := int64(len(r.buf))
	at := int(off % size)
	c := copy(out, r.buf[at:])
	copy(out[c:], r.buf)
	return out, off
}
