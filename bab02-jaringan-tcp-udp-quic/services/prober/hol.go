package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
)

// HOLParams: beberapa stream logis mengirim message terjadwal secara bersamaan.
type HOLParams struct {
	Streams    int `json:"streams"`
	Messages   int `json:"messages"`    // per stream
	IntervalMs int `json:"interval_ms"` // jeda antar message dalam satu stream
	Payload    int `json:"payload"`
}

// HOLSample adalah satu message: dikirim pada T (ms sejak awal), latency Lat.
type HOLSample struct {
	Stream int     `json:"stream"`
	Seq    int     `json:"seq"`
	T      float64 `json:"t_ms"`
	Lat    float64 `json:"lat_ms"`
	Lost   bool    `json:"lost"`
}

// HOLStream ringkasan per stream.
type HOLStream struct {
	Stream int   `json:"stream"`
	Stats  Stats `json:"stats"`
}

// HOLResult hasil untuk satu transport.
type HOLResult struct {
	Transport   string      `json:"transport"`
	Description string      `json:"description"`
	Samples     []HOLSample `json:"samples"`
	PerStream   []HOLStream `json:"per_stream"`
	Stalls      int         `json:"stalls"`
	SharedPct   float64     `json:"shared_pct"` // % message tersendat yang bersamaan dengan stream lain tersendat
	Threshold   float64     `json:"threshold_ms"`
	DurationMs  float64     `json:"duration_ms"`
	Error       string      `json:"error,omitempty"`
}

func (p *HOLParams) normalize() {
	if p.Streams <= 0 {
		p.Streams = 3
	}
	p.Streams = min(p.Streams, 6)
	if p.Messages <= 0 {
		p.Messages = 60
	}
	p.Messages = min(p.Messages, 300)
	if p.IntervalMs <= 0 {
		p.IntervalMs = 30
	}
	p.IntervalMs = min(p.IntervalMs, 500)
	if p.Payload <= 0 {
		p.Payload = 200
	}
	p.Payload = max(32, min(p.Payload, 1000))
}

type schedItem struct {
	stream, seq int
	at          time.Duration
}

// schedule: stream s mengirim message ke-k pada k*interval + s*interval/streams.
func schedule(p HOLParams) []schedItem {
	iv := time.Duration(p.IntervalMs) * time.Millisecond
	var out []schedItem
	for k := 0; k < p.Messages; k++ {
		for s := 0; s < p.Streams; s++ {
			out = append(out, schedItem{s, k, time.Duration(k)*iv + time.Duration(s)*iv/time.Duration(p.Streams)})
		}
	}
	return out
}

type holRecorder struct {
	mu     sync.Mutex
	t0     time.Time
	sendAt map[[2]int]time.Time
	lat    map[[2]int]time.Duration
	total  int
	done   chan struct{}
}

func newRecorder(total int) *holRecorder {
	return &holRecorder{t0: time.Now(), sendAt: map[[2]int]time.Time{}, lat: map[[2]int]time.Duration{},
		total: total, done: make(chan struct{})}
}

func (r *holRecorder) sent(s, k int) {
	r.mu.Lock()
	r.sendAt[[2]int{s, k}] = time.Now()
	r.mu.Unlock()
}

func (r *holRecorder) recv(s, k int) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]int{s, k}
	at, ok := r.sendAt[key]
	if !ok {
		return
	}
	if _, dup := r.lat[key]; dup {
		return
	}
	r.lat[key] = now.Sub(at)
	if len(r.lat) == r.total {
		close(r.done)
	}
}

func (r *holRecorder) result(name, desc string, p HOLParams) HOLResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := HOLResult{Transport: name, Description: desc, DurationMs: ms(time.Since(r.t0))}
	per := make([][]float64, p.Streams)
	for s := 0; s < p.Streams; s++ {
		for k := 0; k < p.Messages; k++ {
			key := [2]int{s, k}
			at, ok := r.sendAt[key]
			if !ok {
				continue
			}
			x := HOLSample{Stream: s, Seq: k, T: ms(at.Sub(r.t0))}
			if l, ok := r.lat[key]; ok {
				x.Lat = ms(l)
				per[s] = append(per[s], x.Lat)
			} else {
				x.Lost = true
			}
			res.Samples = append(res.Samples, x)
		}
	}
	for s := range per {
		res.PerStream = append(res.PerStream, HOLStream{Stream: s, Stats: Summarize(per[s])})
	}
	res.Stalls, res.SharedPct, res.Threshold = stallInfo(res.Samples, float64(p.IntervalMs))
	return res
}

func holMsg(s, k, size int) string { return padMsg(fmt.Sprintf("H %d %d ", s, k), size) }

func parseHOL(line string) (int, int, bool) {
	var s, k int
	if _, err := fmt.Sscanf(line, "H %d %d ", &s, &k); err != nil {
		return 0, 0, false
	}
	return s, k, true
}

func (r *holRecorder) wait(p HOLParams) {
	limit := time.Duration(p.Messages*p.IntervalMs)*time.Millisecond + 15*time.Second
	select {
	case <-r.done:
	case <-time.After(limit):
	}
}

// holTCP: semua stream logis berbagi SATU koneksi TCP (seperti HTTP/2).
func (a *app) holTCP(ctx context.Context, p HOLParams) HOLResult {
	desc := "satu koneksi TCP, stream logis dimultipleks dalam satu byte stream"
	c, err := net.DialTimeout("tcp", a.addr(9000), 10*time.Second)
	if err != nil {
		return HOLResult{Transport: "tcp", Description: desc, Error: err.Error()}
	}
	defer c.Close()
	rec := newRecorder(p.Streams * p.Messages)
	go func() {
		rd := bufio.NewReader(c)
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			if s, k, ok := parseHOL(line); ok {
				rec.recv(s, k)
			}
		}
	}()
	var wmu sync.Mutex
	for _, it := range schedule(p) {
		sleepCtx(ctx, time.Until(rec.t0.Add(it.at)))
		rec.sent(it.stream, it.seq)
		wmu.Lock()
		_, _ = io.WriteString(c, holMsg(it.stream, it.seq, p.Payload))
		wmu.Unlock()
	}
	rec.wait(p)
	return rec.result("tcp", desc, p)
}

// holQUIC: setiap stream logis menjadi stream QUIC sendiri dalam satu koneksi.
func (a *app) holQUIC(ctx context.Context, p HOLParams) HOLResult {
	desc := "satu koneksi QUIC, setiap stream logis = stream QUIC independen"
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(nil), quicConf)
	if err != nil {
		return HOLResult{Transport: "quic", Description: desc, Error: err.Error()}
	}
	defer conn.CloseWithError(0, "")
	streams := make([]*quic.Stream, p.Streams)
	for s := range streams {
		st, err := conn.OpenStreamSync(dctx)
		if err != nil {
			return HOLResult{Transport: "quic", Description: desc, Error: err.Error()}
		}
		_, _ = st.Write([]byte{'E'})
		streams[s] = st
	}
	rec := newRecorder(p.Streams * p.Messages)
	for _, st := range streams {
		go func(st *quic.Stream) {
			rd := bufio.NewReader(st)
			for {
				line, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if s, k, ok := parseHOL(line); ok {
					rec.recv(s, k)
				}
			}
		}(st)
	}
	for _, it := range schedule(p) {
		sleepCtx(ctx, time.Until(rec.t0.Add(it.at)))
		rec.sent(it.stream, it.seq)
		_, _ = io.WriteString(streams[it.stream], holMsg(it.stream, it.seq, p.Payload))
	}
	rec.wait(p)
	for _, st := range streams {
		_ = st.Close()
	}
	return rec.result("quic", desc, p)
}

func (a *app) handleHOL(w http.ResponseWriter, r *http.Request) {
	var p HOLParams
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, 400, "body tidak valid")
		return
	}
	p.normalize()
	if !a.busy.TryLock() {
		httpx.Error(w, 409, "eksperimen lain sedang berjalan")
		return
	}
	defer a.busy.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	nm := a.currentNetem(ctx)
	var tcpRes, quicRes HOLResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); tcpRes = a.holTCP(ctx, p) }()
	go func() { defer wg.Done(); quicRes = a.holQUIC(ctx, p) }()
	wg.Wait()
	a.log.Info("uji HOL selesai", "netem", nm.String(), "tcp_shared_pct", tcpRes.SharedPct, "quic_shared_pct", quicRes.SharedPct,
		"tcp_stalls", tcpRes.Stalls, "quic_stalls", quicRes.Stalls)
	httpx.JSON(w, 200, map[string]any{"params": p, "netem": nm, "netem_text": nm.String(),
		"results": []HOLResult{tcpRes, quicRes}})
}
