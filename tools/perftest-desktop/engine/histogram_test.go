package engine

import (
	"math"
	"testing"
	"time"
)

func near(t *testing.T, name string, got, want time.Duration, tol float64) {
	t.Helper()
	if math.Abs(float64(got-want)) > tol*float64(want) {
		t.Errorf("%s = %v, want %v (±%.0f%%)", name, got, want, tol*100)
	}
}

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogram()
	for i := 1; i <= 10000; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	if h.Count() != 10000 {
		t.Fatalf("count %d", h.Count())
	}
	near(t, "p50", h.Quantile(0.50), 5000*time.Millisecond, 0.01)
	near(t, "p95", h.Quantile(0.95), 9500*time.Millisecond, 0.01)
	near(t, "p99", h.Quantile(0.99), 9900*time.Millisecond, 0.01)
	near(t, "mean", h.Mean(), 5000500*time.Microsecond, 0.0001)
	if h.Min() != time.Millisecond || h.Max() != 10*time.Second {
		t.Errorf("min/max %v %v", h.Min(), h.Max())
	}
	if h.Quantile(0) != h.Min() || h.Quantile(1) != h.Max() {
		t.Error("q0/q1 harus min/max")
	}
}

func TestHistogramSmallAndClamp(t *testing.T) {
	h := NewHistogram()
	h.Record(300 * time.Nanosecond)
	h.Record(-time.Second) // dijepit ke 0
	h.Record(10 * time.Minute)
	if h.Count() != 3 {
		t.Fatal(h.Count())
	}
	if h.Quantile(0.99) != 10*time.Minute {
		t.Errorf("p99 harus dijepit ke max, dapat %v", h.Quantile(0.99))
	}
	e := NewHistogram()
	if e.Quantile(0.5) != 0 || e.Mean() != 0 || len(e.Bins(10)) != 0 {
		t.Error("histogram kosong harus nol")
	}
}

func TestHistogramMergeReset(t *testing.T) {
	a, b := NewHistogram(), NewHistogram()
	for i := 0; i < 100; i++ {
		a.Record(10 * time.Millisecond)
		b.Record(20 * time.Millisecond)
	}
	a.Merge(b)
	if a.Count() != 200 || a.Min() != 10*time.Millisecond || a.Max() != 20*time.Millisecond {
		t.Fatalf("merge salah: %d %v %v", a.Count(), a.Min(), a.Max())
	}
	near(t, "mean", a.Mean(), 15*time.Millisecond, 0.001)
	a.Reset()
	if a.Count() != 0 || a.Max() != 0 {
		t.Error("reset gagal")
	}
}

func TestHistogramBins(t *testing.T) {
	h := NewHistogram()
	for i := 1; i <= 1000; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	bins := h.Bins(20)
	if len(bins) != 20 {
		t.Fatal(len(bins))
	}
	var sum uint64
	for i, b := range bins {
		sum += b.Count
		if b.ToMs < b.FromMs {
			t.Errorf("bin %d terbalik", i)
		}
	}
	if sum != 1000 {
		t.Errorf("jumlah bin %d, want 1000", sum)
	}
	// distribusi seragam: tiap bin kira-kira 50
	if bins[10].Count < 35 || bins[10].Count > 65 {
		t.Errorf("bin tengah %d tidak seragam", bins[10].Count)
	}
}

func TestRecorderTickAndReport(t *testing.T) {
	t0 := time.Unix(1000, 0)
	r := NewRecorder(t0)
	for i := 0; i < 90; i++ {
		r.Add(Sample{Latency: 10 * time.Millisecond})
	}
	for i := 0; i < 10; i++ {
		r.Add(Sample{Err: "http_5xx"})
	}
	p := r.Tick(t0.Add(time.Second))
	if p.OK != 90 || p.Errors != 10 || p.RPS != 100 || p.ErrBy["http_5xx"] != 10 || p.Total != 100 {
		t.Fatalf("point salah: %+v", p)
	}
	p2 := r.Tick(t0.Add(2 * time.Second))
	if p2.OK != 0 || p2.RPS != 0 || p2.ErrBy != nil || p2.Total != 100 {
		t.Fatalf("interval kedua harus kosong: %+v", p2)
	}
	rep := r.Report(Config{Name: "x"}, t0.Add(2*time.Second))
	if rep.Total != 100 || rep.OK != 90 || rep.RPS != 50 || len(rep.Timeline) != 2 {
		t.Fatalf("report salah: %+v", rep)
	}
	if math.Abs(rep.Latency.P99-10) > 0.1 {
		t.Errorf("p99 %v", rep.Latency.P99)
	}
}
