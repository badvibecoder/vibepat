package main

// RingBuffer is a fixed-capacity FIFO circular buffer of strings used to retain
// the preceding context of a match without unbounded allocation.
//
// The zero value is not usable; construct one with NewRingBuffer. A capacity of
// zero (or less) is legal and means "never retain anything", which is the
// default for vibepat's --context flag. Push on a zero-capacity buffer is a
// no-op.
type RingBuffer struct {
	buf   []string
	head  int // index of the oldest element
	count int // number of valid elements, 0 <= count <= cap(buf)
}

// NewRingBuffer returns a RingBuffer holding at most capacity strings. If
// capacity is zero or negative the buffer is permanently empty rather than
// panicking, because --context 0 is a legitimate and common invocation.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity < 0 {
		capacity = 0
	}
	return &RingBuffer{buf: make([]string, capacity)}
}

// Cap reports the buffer's maximum number of retained entries.
func (r *RingBuffer) Cap() int { return len(r.buf) }

// Len reports how many entries are currently retained.
func (r *RingBuffer) Len() int { return r.count }

// Push appends value, evicting the oldest entry when the buffer is already at
// capacity. This is O(1) and allocates nothing after construction.
func (r *RingBuffer) Push(value string) {
	if len(r.buf) == 0 {
		return
	}
	if r.count < len(r.buf) {
		r.buf[(r.head+r.count)%len(r.buf)] = value
		r.count++
		return
	}
	// Full: overwrite the oldest slot and advance the head past it.
	r.buf[r.head] = value
	r.head = (r.head + 1) % len(r.buf)
}

// GetContext returns the retained entries in chronological order, oldest first,
// in a freshly allocated slice. The caller owns the result and may mutate it
// freely. It is always non-nil so callers can serialize it as [] not null.
//
// Callers wanting "the N lines before the current one" must call GetContext
// before pushing the current line.
func (r *RingBuffer) GetContext() []string {
	out := make([]string, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(r.head+i)%len(r.buf)]
	}
	return out
}

// Reset discards every retained entry without reallocating. Pushes after Reset
// behave as if the buffer were newly constructed.
func (r *RingBuffer) Reset() {
	r.head = 0
	r.count = 0
}
