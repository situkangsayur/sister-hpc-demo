// Package trace mencatat panggilan socket API (accept, recv, send, close) per
// koneksi agar UI dapat menggambar timeline satu koneksi.
package trace

import (
	"sync"
	"time"
)

// Event adalah satu panggilan socket API.
type Event struct {
	TS     float64 `json:"ts"`   // detik Unix
	Side   string  `json:"side"` // client | server
	Call   string  `json:"call"` // socket(), connect(), accept(), send(), recv(), close() ...
	Bytes  int     `json:"bytes"`
	Detail string  `json:"detail"`
}

// Now mengembalikan waktu sekarang dalam detik Unix (presisi mikrodetik).
func Now() float64 { return float64(time.Now().UnixMicro()) / 1e6 }

// Ring menyimpan event untuk paling banyak maxKeys koneksi terakhir.
type Ring struct {
	mu      sync.Mutex
	maxKeys int
	order   []string
	events  map[string][]Event
}

// NewRing membuat Ring baru.
func NewRing(maxKeys int) *Ring {
	return &Ring{maxKeys: maxKeys, events: map[string][]Event{}}
}

// Add mencatat event untuk koneksi key (biasanya alamat remote "ip:port").
func (r *Ring) Add(key string, e Event) {
	if e.TS == 0 {
		e.TS = Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.events[key]; !ok {
		r.order = append(r.order, key)
		if len(r.order) > r.maxKeys {
			delete(r.events, r.order[0])
			r.order = r.order[1:]
		}
	}
	if len(r.events[key]) < 200 {
		r.events[key] = append(r.events[key], e)
	}
}

// Get mengembalikan salinan event untuk key.
func (r *Ring) Get(key string) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events[key]...)
}
