package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"sister-bab05/services/platform"
)

// Lab delivery semantics:
//   - lab.core.task (core NATS): dua worker dalam queue group "lab-workers" (satu pesan ke satu
//     worker) ditambah satu subscriber biasa "audit-log" (fan-out, menerima semua). Core NATS
//     tidak menyimpan pesan: saat penerima offline, pesan hilang (at-most-once).
//   - lab.js.task (JetStream, stream LAB): durable consumer "lab-worker" dengan ack. Saat
//     offline pesan tersimpan di stream dan dikirim begitu penerima kembali (at-least-once).
type lab struct {
	natsURL string
	js      jetstream.JetStream
	log     *slog.Logger
	offline atomic.Bool
	fetchMu sync.Mutex // dipegang selama satu putaran fetch JetStream

	mu       sync.Mutex
	conns    []*nats.Conn
	received map[string]int
}

const labCoreSubject = "lab.core.task"

func newLab(ctx context.Context, natsURL string, js jetstream.JetStream, log *slog.Logger) (*lab, error) {
	l := &lab{natsURL: natsURL, js: js, log: log, received: map[string]int{}}
	if err := platform.EnsureStream(ctx, js, "LAB", []string{"lab.js.>"}); err != nil {
		return nil, err
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, "LAB", jetstream.ConsumerConfig{Durable: "lab-worker",
		AckPolicy: jetstream.AckExplicitPolicy, AckWait: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := l.subscribeCore(); err != nil {
		return nil, err
	}
	go func() {
		for ctx.Err() == nil {
			if l.offline.Load() {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			l.fetchMu.Lock()
			batch, err := cons.Fetch(20, jetstream.FetchMaxWait(time.Second))
			if err == nil {
				for m := range batch.Messages() {
					l.count("js-worker")
					_ = m.Ack() // proses dulu (menghitung), baru ack
				}
			}
			l.fetchMu.Unlock()
			if err != nil {
				time.Sleep(300 * time.Millisecond)
			}
		}
	}()
	return l, nil
}

// subscribeCore membuka tiga koneksi terpisah agar terlihat seperti tiga proses penerima.
func (l *lab) subscribeCore() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, name := range []string{"worker-a", "worker-b", "audit-log"} {
		nc, err := nats.Connect(l.natsURL, nats.Name("lab-"+name))
		if err != nil {
			return err
		}
		n := name
		h := func(*nats.Msg) { l.count(n) }
		if name == "audit-log" {
			_, err = nc.Subscribe(labCoreSubject, h)
		} else {
			_, err = nc.QueueSubscribe(labCoreSubject, "lab-workers", h)
		}
		if err != nil {
			return err
		}
		_ = nc.Flush()
		l.conns = append(l.conns, nc)
	}
	return nil
}

func (l *lab) closeCore() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.Close()
	}
	l.conns = nil
}

func (l *lab) count(name string) {
	l.mu.Lock()
	l.received[name]++
	l.mu.Unlock()
}

func (l *lab) stats() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := map[string]int{}
	for k, v := range l.received {
		r[k] = v
	}
	return map[string]any{"offline": l.offline.Load(), "received": r}
}

func (l *lab) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /lab/stats", func(w http.ResponseWriter, r *http.Request) { platform.WriteJSON(w, 200, l.stats()) })
	mux.HandleFunc("POST /lab/offline", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Offline bool `json:"offline"`
		}
		if err := platform.ReadJSON(r, &in); err != nil {
			platform.WriteJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if in.Offline && !l.offline.Load() {
			l.closeCore() // subscriber core hilang: NATS tidak punya tempat menitipkan pesan
		} else if !in.Offline && l.offline.Load() {
			if err := l.subscribeCore(); err != nil {
				platform.WriteJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
		}
		l.offline.Store(in.Offline)
		// Tunggu putaran fetch yang sedang berjalan selesai, agar setelah respons ini
		// penerima benar-benar tidak mengambil pesan lagi.
		l.fetchMu.Lock()
		l.fetchMu.Unlock() //nolint:staticcheck
		l.log.Warn("lab penerima", "request_id", platform.RequestID(r.Context()), "offline", in.Offline)
		platform.WriteJSON(w, 200, l.stats())
	})
	mux.HandleFunc("POST /lab/reset", func(w http.ResponseWriter, r *http.Request) {
		if s, err := l.js.Stream(r.Context(), "LAB"); err == nil {
			_ = s.Purge(r.Context())
		}
		l.mu.Lock()
		l.received = map[string]int{}
		l.mu.Unlock()
		platform.WriteJSON(w, 200, l.stats())
	})
}
