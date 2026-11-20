package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/netem"
)

// RunParams adalah parameter satu putaran perbandingan transport.
type RunParams struct {
	Count      int `json:"count"`       // jumlah message per protokol
	IntervalMs int `json:"interval_ms"` // jeda antar message
	Payload    int `json:"payload"`     // ukuran message (byte)
	BulkKB     int `json:"bulk_kb"`     // data uji throughput; 0 = lewati
}

func (p *RunParams) normalize() {
	if p.Count <= 0 {
		p.Count = 100
	}
	p.Count = min(p.Count, 500)
	p.IntervalMs = max(0, min(p.IntervalMs, 1000))
	if p.Payload <= 0 {
		p.Payload = 64
	}
	p.Payload = max(16, min(p.Payload, 1200))
	p.BulkKB = max(0, min(p.BulkKB, 8192))
}

// Sample adalah satu message: RTT-nya, atau hilang.
type Sample struct {
	Seq  int     `json:"seq"`
	RTT  float64 `json:"rtt_ms"`
	Lost bool    `json:"lost"`
	T    float64 `json:"t_ms"` // waktu kirim sejak awal uji
}

// Throughput hasil uji kirim data besar.
type Throughput struct {
	SentBytes      int64   `json:"sent_bytes"`
	DeliveredBytes int64   `json:"delivered_bytes"`
	Ms             float64 `json:"ms"`
	Mbps           float64 `json:"mbps"`
	LossPct        float64 `json:"loss_pct"`
	Note           string  `json:"note,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// ProtoResult adalah hasil satu protokol dalam satu putaran.
type ProtoResult struct {
	Proto      string      `json:"proto"`
	Status     string      `json:"status"` // pending | running | done | error
	Error      string      `json:"error,omitempty"`
	Sent       int         `json:"sent"`
	Received   int         `json:"received"`
	LossPct    float64     `json:"loss_pct"`
	Stats      Stats       `json:"stats"`
	Samples    []Sample    `json:"samples"`
	Throughput *Throughput `json:"throughput,omitempty"`
}

// Handshake adalah waktu sampai data aplikasi boleh dikirim dan sampai balasan pertama.
type Handshake struct {
	Name         string    `json:"name"`
	HandshakeMs  float64   `json:"handshake_ms"`
	FirstReplyMs float64   `json:"first_reply_ms"`
	Runs         []float64 `json:"runs"`
	TheoryRTT    string    `json:"theory_rtt"`
	Note         string    `json:"note,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Run adalah satu putaran lengkap.
type Run struct {
	mu         sync.Mutex
	ID         int            `json:"id"`
	Status     string         `json:"status"` // running | done
	Phase      string         `json:"phase"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Params     RunParams      `json:"params"`
	Netem      netem.Params   `json:"netem"`
	NetemText  string         `json:"netem_text"`
	Protocols  []*ProtoResult `json:"protocols"`
	Handshakes []Handshake    `json:"handshakes"`
}

// RunSummary dipakai untuk tabel pencatatan seperti Tabel 2.x di buku.
type RunSummary struct {
	ID        int                `json:"id"`
	NetemText string             `json:"netem_text"`
	Params    RunParams          `json:"params"`
	Protos    map[string]Summary `json:"protos"`
}

// Summary ringkas per protokol.
type Summary struct {
	Median  float64 `json:"median"`
	Max     float64 `json:"max"`
	P95     float64 `json:"p95"`
	LossPct float64 `json:"loss_pct"`
	Mbps    float64 `json:"mbps"`
}

func (r *Run) proto(name string) *ProtoResult {
	for _, p := range r.Protocols {
		if p.Proto == name {
			return p
		}
	}
	return nil
}

func (r *Run) summary() RunSummary {
	s := RunSummary{ID: r.ID, NetemText: r.NetemText, Params: r.Params, Protos: map[string]Summary{}}
	for _, p := range r.Protocols {
		x := Summary{Median: p.Stats.Median, Max: p.Stats.Max, P95: p.Stats.P95, LossPct: p.LossPct}
		if p.Throughput != nil {
			x.Mbps = p.Throughput.Mbps
		}
		s.Protos[p.Proto] = x
	}
	return s
}

func (a *app) handleRunStart(w http.ResponseWriter, r *http.Request) {
	var p RunParams
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, 400, "body tidak valid")
		return
	}
	p.normalize()
	a.mu.Lock()
	if a.current != nil {
		a.current.mu.Lock()
		running := a.current.Status == "running"
		a.current.mu.Unlock()
		if running {
			a.mu.Unlock()
			httpx.Error(w, 409, "masih ada putaran yang berjalan")
			return
		}
	}
	a.nextID++
	nm := a.currentNetem(r.Context())
	run := &Run{ID: a.nextID, Status: "running", Phase: "latency", StartedAt: time.Now(), Params: p,
		Netem: nm, NetemText: nm.String()}
	for _, name := range []string{"tcp", "udp", "quic"} {
		run.Protocols = append(run.Protocols, &ProtoResult{Proto: name, Status: "pending", Samples: []Sample{}})
	}
	a.current = run
	a.mu.Unlock()

	go a.execute(run)
	a.log.Info("putaran dimulai", "run", run.ID, "netem", run.NetemText, "count", p.Count)
	a.writeRun(w, run)
}

func (a *app) handleRunGet(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	run := a.current
	a.mu.Unlock()
	if run == nil {
		httpx.JSON(w, 200, map[string]any{"id": 0, "status": "idle"})
		return
	}
	a.writeRun(w, run)
}

func (a *app) writeRun(w http.ResponseWriter, run *Run) {
	run.mu.Lock()
	b, err := json.Marshal(run)
	run.mu.Unlock()
	if err != nil {
		httpx.Error(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// execute menjalankan tiga fase: latency (tiga protokol bersamaan), handshake, throughput.
func (a *app) execute(run *Run) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t0 := time.Now()
	var wg sync.WaitGroup
	for _, name := range []string{"tcp", "udp", "quic"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			pr := run.proto(name)
			run.mu.Lock()
			pr.Status = "running"
			run.mu.Unlock()
			var err error
			switch name {
			case "tcp":
				err = a.tcpLatency(ctx, run, pr, t0)
			case "udp":
				err = a.udpLatency(ctx, run, pr, t0)
			case "quic":
				err = a.quicLatency(ctx, run, pr, t0)
			}
			run.mu.Lock()
			finishProto(pr, err)
			run.mu.Unlock()
		}(name)
	}
	wg.Wait()

	run.mu.Lock()
	run.Phase = "handshake"
	run.mu.Unlock()
	hs := a.handshakes(ctx)
	run.mu.Lock()
	run.Handshakes = hs
	run.Phase = "throughput"
	run.mu.Unlock()

	if run.Params.BulkKB > 0 {
		for _, name := range []string{"tcp", "udp", "quic"} {
			var tp Throughput
			switch name {
			case "tcp":
				tp = a.tcpThroughput(ctx, run.Params.BulkKB)
			case "udp":
				tp = a.udpThroughput(ctx, run.Params.BulkKB)
			case "quic":
				tp = a.quicThroughput(ctx, run.Params.BulkKB)
			}
			run.mu.Lock()
			run.proto(name).Throughput = &tp
			run.mu.Unlock()
		}
	}

	run.mu.Lock()
	now := time.Now()
	run.Status, run.Phase, run.FinishedAt = "done", "selesai", &now
	sum := run.summary()
	run.mu.Unlock()
	a.mu.Lock()
	a.history = append([]RunSummary{sum}, a.history...)
	if len(a.history) > 12 {
		a.history = a.history[:12]
	}
	a.mu.Unlock()
	a.log.Info("putaran selesai", "run", run.ID, "netem", run.NetemText,
		"tcp_median", sum.Protos["tcp"].Median, "tcp_max", sum.Protos["tcp"].Max,
		"udp_median", sum.Protos["udp"].Median, "udp_loss_pct", sum.Protos["udp"].LossPct,
		"quic_median", sum.Protos["quic"].Median)
}

func finishProto(pr *ProtoResult, err error) {
	var rtts []float64
	pr.Received = 0
	for _, s := range pr.Samples {
		if !s.Lost {
			rtts = append(rtts, s.RTT)
			pr.Received++
		}
	}
	pr.Stats = Summarize(rtts)
	if pr.Sent > 0 {
		pr.LossPct = round(100 * float64(pr.Sent-pr.Received) / float64(pr.Sent))
	}
	pr.Status = "done"
	if err != nil {
		pr.Status, pr.Error = "error", err.Error()
	}
}
