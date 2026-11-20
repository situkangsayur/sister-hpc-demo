package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Eksperimen 2: deadlock (Subbab "Deadlock"). Beberapa thread mentransfer dana antarrekening;
// setiap transfer butuh dua lock. lockMgr mencatat siapa memegang dan siapa menunggu, sehingga
// wait-for graph bisa digambar dan siklusnya dideteksi.

type lockMgr struct {
	mu      sync.Mutex
	locks   map[string]chan struct{} // channel ber-buffer 1 = mutex yang bisa dibatalkan
	holder  map[string]string        // lock -> thread
	waiting map[string]string        // thread -> lock yang ditunggu
	status  map[string]string        // thread -> status
}

func newLockMgr(names []string) *lockMgr {
	m := &lockMgr{locks: map[string]chan struct{}{}, holder: map[string]string{},
		waiting: map[string]string{}, status: map[string]string{}}
	for _, n := range names {
		m.locks[n] = make(chan struct{}, 1)
	}
	return m
}

func (m *lockMgr) setStatus(th, s string) {
	m.mu.Lock()
	m.status[th] = s
	m.mu.Unlock()
}

// acquire menunggu lock sampai didapat atau ctx dibatalkan.
func (m *lockMgr) acquire(ctx context.Context, th, l string) error {
	return m.acquireTimeout(ctx, th, l, 0)
}

// acquireTimeout seperti acquire, tetapi menyerah setelah timeout (bila timeout > 0).
// Inilah tryLock dengan timeout yang mematahkan syarat hold-and-wait.
func (m *lockMgr) acquireTimeout(ctx context.Context, th, l string, timeout time.Duration) error {
	m.mu.Lock()
	m.waiting[th] = l
	m.status[th] = "waiting"
	m.mu.Unlock()
	var tc <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		tc = t.C
	}
	select {
	case m.locks[l] <- struct{}{}:
		m.mu.Lock()
		delete(m.waiting, th)
		m.holder[l] = th
		m.status[th] = "running"
		m.mu.Unlock()
		return nil
	case <-tc:
		m.mu.Lock()
		delete(m.waiting, th)
		m.mu.Unlock()
		return errTimeout
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.waiting, th)
		m.mu.Unlock()
		return ctx.Err()
	}
}

var errTimeout = fmt.Errorf("timeout")

func (m *lockMgr) release(th, l string) {
	m.mu.Lock()
	if m.holder[l] == th {
		delete(m.holder, l)
	}
	m.mu.Unlock()
	<-m.locks[l]
}

type dlThread struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Holds    []string `json:"holds"`
	WaitsFor string   `json:"waits_for,omitempty"`
}

type dlEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // "holds" (lock -> thread) atau "waits" (thread -> lock)
}

type dlSnapshot struct {
	TMs     int64      `json:"t_ms"`
	Threads []dlThread `json:"threads"`
	Locks   []string   `json:"locks"`
	Edges   []dlEdge   `json:"edges"`
	// WaitFor adalah wait-for graph antar-thread: A -> B berarti A menunggu lock yang dipegang B.
	WaitFor []dlEdge `json:"wait_for"`
	Cycle   []string `json:"cycle"`
}

func (m *lockMgr) snapshot(threads, locks []string, t0 time.Time) dlSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := dlSnapshot{TMs: time.Since(t0).Milliseconds(), Locks: locks}
	wf := map[string]string{}
	for _, th := range threads {
		t := dlThread{ID: th, Status: m.status[th], Holds: []string{}}
		for _, l := range locks {
			if m.holder[l] == th {
				t.Holds = append(t.Holds, l)
				s.Edges = append(s.Edges, dlEdge{From: l, To: th, Kind: "holds"})
			}
		}
		if l, ok := m.waiting[th]; ok {
			t.WaitsFor = l
			s.Edges = append(s.Edges, dlEdge{From: th, To: l, Kind: "waits"})
			if h, ok := m.holder[l]; ok && h != th {
				wf[th] = h
				s.WaitFor = append(s.WaitFor, dlEdge{From: th, To: h, Kind: "waits-for"})
			}
		}
		s.Threads = append(s.Threads, t)
	}
	s.Cycle = findCycle(wf, threads)
	return s
}

// findCycle mencari siklus di wait-for graph. Setiap thread menunggu paling banyak satu lock,
// sehingga setiap simpul punya paling banyak satu sisi keluar: cukup ikuti penunjuknya.
func findCycle(wf map[string]string, order []string) []string {
	for _, start := range order {
		seen := map[string]int{}
		path := []string{}
		cur := start
		for {
			if i, ok := seen[cur]; ok {
				return path[i:]
			}
			next, ok := wf[cur]
			if !ok {
				break
			}
			seen[cur] = len(path)
			path = append(path, cur)
			cur = next
		}
	}
	return nil
}

var dlStrategies = []string{"naive", "ordered", "trylock", "trylock-nojitter"}

func handleDeadlockStream(w http.ResponseWriter, r *http.Request) {
	strategy := r.URL.Query().Get("strategy")
	if !slices.Contains(dlStrategies, strategy) {
		strategy = "naive"
	}
	k := qInt(r, "threads", 2, 2, 4)
	rounds := qInt(r, "rounds", 3, 1, 10)
	hold := time.Duration(qInt(r, "hold_ms", 300, 20, 2000)) * time.Millisecond

	sse, err := newSSE(w)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	accts := []string{"rekA", "rekB", "rekC", "rekD"}[:k]
	threads := make([]string, k)
	for i := range threads {
		threads[i] = fmt.Sprintf("T%d", i+1)
	}
	m := newLockMgr(accts)
	saldo := map[string]int64{}
	for _, a := range accts {
		saldo[a] = 1000
	}
	var transfers, retries atomic.Int64
	t0 := time.Now()
	logf := func(format string, a ...any) {
		_ = sse.send("log", map[string]any{"t_ms": time.Since(t0).Milliseconds(), "msg": fmt.Sprintf(format, a...)})
	}
	_ = sse.send("start", map[string]any{"strategy": strategy, "threads": threads, "locks": accts,
		"rounds": rounds, "hold_ms": hold.Milliseconds()})
	logger.Info("deadlock mulai", "request_id", reqID(r), "strategy", strategy, "threads", k)

	sleep := func(d time.Duration) bool {
		select {
		case <-time.After(d):
			return true
		case <-ctx.Done():
			return false
		}
	}

	var wg sync.WaitGroup
	for i, th := range threads {
		from, to := accts[i], accts[(i+1)%k]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if ctx.Err() != nil {
					m.setStatus(th, "aborted")
				} else {
					m.setStatus(th, "done")
				}
			}()
			for round := 0; round < rounds; round++ {
				first, second := from, to
				if strategy == "ordered" && second < first {
					first, second = second, first // urutan global: nama lock terkecil dulu
				}
				for {
					if err := m.acquire(ctx, th, first); err != nil {
						return
					}
					logf("%s memegang %s, perlu %s", th, first, second)
					if !sleep(hold) {
						m.release(th, first)
						return
					}
					var err error
					if strategy == "trylock" || strategy == "trylock-nojitter" {
						err = m.acquireTimeout(ctx, th, second, hold)
					} else {
						err = m.acquire(ctx, th, second)
					}
					if err == errTimeout {
						m.release(th, first)
						retries.Add(1)
						m.setStatus(th, "backoff")
						back := hold / 2
						if strategy == "trylock" {
							back = time.Duration(rand.Int64N(int64(hold)*3/2)) + 20*time.Millisecond // jitter
						}
						logf("%s gagal dapat %s, lepas %s, mundur %d ms", th, second, first, back.Milliseconds())
						if !sleep(back) {
							return
						}
						continue
					}
					if err != nil {
						m.release(th, first)
						return
					}
					break
				}
				// critical section: kedua lock dipegang
				m.mu.Lock()
				saldo[from] -= 100
				saldo[to] += 100
				m.mu.Unlock()
				transfers.Add(1)
				logf("%s transfer 100 dari %s ke %s (putaran %d)", th, from, to, round+1)
				sleep(hold / 3)
				m.release(th, second)
				m.release(th, first)
				m.setStatus(th, "running")
				if !sleep(time.Duration(rand.Int64N(int64(hold) / 4))) {
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var cycleSince time.Time
	detected := false
	status := "selesai"
loop:
	for {
		select {
		case <-done:
			break loop
		case <-tick.C:
			s := m.snapshot(threads, accts, t0)
			_ = sse.send("state", s)
			if len(s.Cycle) > 0 {
				if cycleSince.IsZero() {
					cycleSince = time.Now()
				}
				if !detected && time.Since(cycleSince) > 1500*time.Millisecond {
					detected = true
					status = "deadlock"
					logf("DEADLOCK: siklus %v bertahan > 1,5 detik. Detektor membatalkan semua thread dalam 2 detik.", s.Cycle)
					_ = sse.send("detected", map[string]any{"cycle": s.Cycle, "t_ms": s.TMs})
					logger.Warn("deadlock terdeteksi", "request_id", reqID(r), "cycle", s.Cycle)
					time.AfterFunc(2*time.Second, cancel)
				}
			} else {
				cycleSince = time.Time{}
			}
		}
	}
	if !detected && ctx.Err() != nil {
		if r.Context().Err() != nil {
			return // klien menutup stream
		}
		status = "timeout"
		if retries.Load() > 0 {
			status = "livelock" // terus mengalah dan mencoba ulang tanpa kemajuan berarti
		}
	}
	m.mu.Lock()
	var total int64
	for _, v := range saldo {
		total += v
	}
	sal := map[string]int64{}
	for kk, v := range saldo {
		sal[kk] = v
	}
	m.mu.Unlock()
	_ = sse.send("state", m.snapshot(threads, accts, t0))
	_ = sse.send("done", map[string]any{"status": status, "duration_ms": time.Since(t0).Milliseconds(),
		"transfers": transfers.Load(), "expected_transfers": k * rounds, "retries": retries.Load(),
		"saldo": sal, "total": total, "expected_total": int64(1000 * k)})
	logger.Info("deadlock selesai", "request_id", reqID(r), "strategy", strategy, "status", status,
		"transfers", transfers.Load(), "retries", retries.Load())
}
