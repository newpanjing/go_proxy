package logstream

import (
	"bytes"
	"sync"
	"time"
)

type Entry struct {
	Time       time.Time `json:"time"`
	Message    string    `json:"message"`
	Kind       string    `json:"kind,omitempty"`
	Listen     string    `json:"listen,omitempty"`
	Source     string    `json:"source,omitempty"`
	Target     string    `json:"target,omitempty"`
	Bytes      uint64    `json:"bytes,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

type Hub struct {
	mu          sync.RWMutex
	entries     []Entry
	subscribers map[chan Entry]struct{}
	limit       int
}

func NewHub(limit int) *Hub {
	if limit <= 0 {
		limit = 100
	}
	return &Hub{
		subscribers: make(map[chan Entry]struct{}),
		limit:       limit,
	}
}

var Default = NewHub(200)

func (h *Hub) Add(message string) {
	h.AddEntry(Entry{Message: message})
}

func (h *Hub) AddEntry(entry Entry) {
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	if len(h.entries) > h.limit {
		h.entries = h.entries[len(h.entries)-h.limit:]
	}
	for ch := range h.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *Hub) Recent() []Entry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Entry, len(h.entries))
	copy(out, h.entries)
	return out
}

func (h *Hub) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 32)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

type Writer struct {
	hub *Hub
	buf bytes.Buffer
	mu  sync.Mutex
}

func NewWriter(hub *Hub) *Writer {
	return &Writer{hub: hub}
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, b := range p {
		if b == '\n' {
			w.flush()
			continue
		}
		_ = w.buf.WriteByte(b)
	}
	return len(p), nil
}

func (w *Writer) flush() {
	if w.buf.Len() == 0 {
		return
	}
	w.hub.Add(w.buf.String())
	w.buf.Reset()
}
