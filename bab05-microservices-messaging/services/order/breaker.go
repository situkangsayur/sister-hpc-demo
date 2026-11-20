package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/sony/gobreaker"

	"sister-bab05/services/platform"
)

// remote membungkus panggilan synchronous ke service lain dengan timeout, retry
// (exponential backoff + full jitter), dan circuit breaker (sony/gobreaker).
type remote struct {
	name     string
	baseURL  string
	timeout  time.Duration
	attempts int
	cb       *gobreaker.CircuitBreaker
	client   *http.Client

	mu          sync.Mutex
	lastChange  time.Time
	transitions []string
}

func newRemote(name, baseURL string, timeout time.Duration, attempts int, nc *nats.Conn) *remote {
	r := &remote{name: name, baseURL: baseURL, timeout: timeout, attempts: attempts,
		client: &http.Client{}, lastChange: time.Now()}
	r.cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,                // satu panggilan uji saat half-open
		Interval:    30 * time.Second, // jendela hitung saat closed
		Timeout:     10 * time.Second, // lama state open sebelum half-open
		ReadyToTrip: func(c gobreaker.Counts) bool {
			// Ambang demo dibuat kecil (buku: 50% dari minimal 20 panggilan).
			return c.Requests >= 4 && float64(c.TotalFailures)/float64(c.Requests) >= 0.5
		},
		// 4xx bukan tanda tetangga sakit, jadi tidak dihitung gagal oleh breaker.
		IsSuccessful: func(err error) bool {
			var hs httpStatusError
			return err == nil || (errors.As(err, &hs) && hs.code < 500)
		},
		OnStateChange: func(n string, from, to gobreaker.State) {
			r.mu.Lock()
			r.lastChange = time.Now()
			r.transitions = append(r.transitions, fmt.Sprintf("%s %s->%s", time.Now().Format("15:04:05"), from, to))
			if len(r.transitions) > 8 {
				r.transitions = r.transitions[1:]
			}
			r.mu.Unlock()
			platform.Audit(nc, "order", map[string]any{"action": "breaker", "breaker": n,
				"note": fmt.Sprintf("circuit breaker %s: %s -> %s", n, from, to), "from": from.String(), "to": to.String()})
		},
	})
	return r
}

type httpStatusError struct{ code int }

func (e httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

// get memanggil GET baseURL+path dan mengembalikan body. Setiap percobaan melewati breaker;
// bila breaker open, retry dihentikan (ErrPermanent) dan pemanggil memakai fallback.
func (r *remote) get(ctx context.Context, path string) ([]byte, int, error) {
	var body []byte
	tries := 0
	err := platform.Retry(ctx, r.attempts, 50*time.Millisecond, 400*time.Millisecond, func(int) error {
		tries++
		res, err := r.cb.Execute(func() (any, error) {
			cctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			req, _ := http.NewRequestWithContext(cctx, http.MethodGet, r.baseURL+path, nil)
			req.Header.Set(platform.HeaderRequestID, platform.RequestID(ctx)) // teruskan request id
			resp, err := r.client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode >= 500 {
				return nil, httpStatusError{resp.StatusCode}
			}
			if resp.StatusCode >= 400 {
				return b, httpStatusError{resp.StatusCode}
			}
			return b, nil
		})
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return errors.Join(platform.ErrPermanent, fmt.Errorf("breaker %s %s", r.name, r.cb.State()))
		}
		var hs httpStatusError
		if errors.As(err, &hs) && hs.code < 500 {
			body, _ = res.([]byte)
			return errors.Join(platform.ErrPermanent, err)
		}
		if err != nil {
			return err
		}
		body = res.([]byte)
		return nil
	})
	return body, tries, err
}

// breakerView adalah status breaker untuk UI.
type breakerView struct {
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Requests    uint32   `json:"requests"`
	Failures    uint32   `json:"failures"`
	Successes   uint32   `json:"successes"`
	ConsecFail  uint32   `json:"consecutive_failures"`
	SinceMs     int64    `json:"since_ms"`
	Timeout     string   `json:"timeout"`
	Attempts    int      `json:"attempts"`
	Transitions []string `json:"transitions"`
}

func (r *remote) view() breakerView {
	// Baca state dan counts SEBELUM mengunci r.mu: OnStateChange dipanggil gobreaker sambil
	// memegang mutex internalnya lalu mengunci r.mu; urutan kunci terbalik akan deadlock.
	c := r.cb.Counts()
	state := r.cb.State().String()
	r.mu.Lock()
	defer r.mu.Unlock()
	return breakerView{Name: r.name, State: state, Requests: c.Requests, Failures: c.TotalFailures,
		Successes: c.TotalSuccesses, ConsecFail: c.ConsecutiveFailures, SinceMs: time.Since(r.lastChange).Milliseconds(),
		Timeout: r.timeout.String(), Attempts: r.attempts, Transitions: append([]string{}, r.transitions...)}
}

// isStatus memeriksa apakah error berasal dari kode HTTP tertentu.
func isStatus(err error, code int) bool {
	var hs httpStatusError
	return errors.As(err, &hs) && hs.code == code
}
