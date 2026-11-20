package engine

import (
	"maps"
	"sync"
	"time"
)

// Sample adalah hasil satu request. Err kosong berarti sukses; selain itu berisi jenis error
// (lihat Classify).
type Sample struct {
	Latency time.Duration
	Err     string
}

// Point adalah ringkasan satu interval (biasanya 1 detik) untuk grafik live.
type Point struct {
	T      float64           `json:"t"` // detik sejak mulai (akhir interval)
	RPS    float64           `json:"rps"`
	OK     uint64            `json:"ok"`
	Errors uint64            `json:"errors"`
	P50    float64           `json:"p50Ms"`
	P95    float64           `json:"p95Ms"`
	P99    float64           `json:"p99Ms"`
	Mean   float64           `json:"meanMs"`
	ErrBy  map[string]uint64 `json:"errorsByKind,omitempty"`
	Total  uint64            `json:"total"`  // kumulatif sampai titik ini
	ErrSum uint64            `json:"errSum"` // kumulatif error sampai titik ini
}

// LatencySummary adalah statistik latency request yang sukses (milidetik).
type LatencySummary struct {
	Min  float64 `json:"minMs"`
	Mean float64 `json:"meanMs"`
	P50  float64 `json:"p50Ms"`
	P90  float64 `json:"p90Ms"`
	P95  float64 `json:"p95Ms"`
	P99  float64 `json:"p99Ms"`
	Max  float64 `json:"maxMs"`
}

// Report adalah hasil akhir satu run.
type Report struct {
	Config       Config            `json:"config"`
	StartedAt    time.Time         `json:"startedAt"`
	ElapsedS     float64           `json:"elapsedS"`
	Total        uint64            `json:"total"`
	OK           uint64            `json:"ok"`
	Errors       uint64            `json:"errors"`
	ErrorsByKind map[string]uint64 `json:"errorsByKind"`
	RPS          float64           `json:"rps"` // throughput rata-rata (semua request)
	Latency      LatencySummary    `json:"latency"`
	Timeline     []Point           `json:"timeline"`
	Histogram    []Bin             `json:"histogram"`
	Stopped      bool              `json:"stopped"` // dihentikan pengguna sebelum selesai
}

// Recorder mengumpulkan Sample dari banyak worker secara aman (mutex) dan menghasilkan Point
// per interval serta Report akhir.
type Recorder struct {
	mu       sync.Mutex
	start    time.Time
	lastTick time.Time
	all      *Histogram
	iv       *Histogram
	ok, errs uint64
	ivErrs   uint64
	errBy    map[string]uint64
	ivErrBy  map[string]uint64
	timeline []Point
}

// NewRecorder membuat Recorder yang mulai dihitung pada start.
func NewRecorder(start time.Time) *Recorder {
	return &Recorder{
		start: start, lastTick: start,
		all: NewHistogram(), iv: NewHistogram(),
		errBy: map[string]uint64{}, ivErrBy: map[string]uint64{},
	}
}

// Add mencatat satu sampel. Latency hanya dihitung untuk request sukses.
func (r *Recorder) Add(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Err == "" {
		r.ok++
		r.all.Record(s.Latency)
		r.iv.Record(s.Latency)
		return
	}
	r.errs++
	r.ivErrs++
	r.errBy[s.Err]++
	r.ivErrBy[s.Err]++
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Tick menutup interval sejak Tick sebelumnya, menyimpannya ke timeline, dan mengembalikannya.
func (r *Recorder) Tick(now time.Time) Point {
	r.mu.Lock()
	defer r.mu.Unlock()
	dt := now.Sub(r.lastTick).Seconds()
	n := r.iv.Count() + r.ivErrs
	p := Point{
		T:      now.Sub(r.start).Seconds(),
		OK:     r.iv.Count(),
		Errors: r.ivErrs,
		P50:    ms(r.iv.Quantile(0.50)),
		P95:    ms(r.iv.Quantile(0.95)),
		P99:    ms(r.iv.Quantile(0.99)),
		Mean:   ms(r.iv.Mean()),
		Total:  r.ok + r.errs,
		ErrSum: r.errs,
	}
	if dt > 0 {
		p.RPS = float64(n) / dt
	}
	if len(r.ivErrBy) > 0 {
		p.ErrBy = maps.Clone(r.ivErrBy)
	}
	r.timeline = append(r.timeline, p)
	r.iv.Reset()
	r.ivErrs = 0
	clear(r.ivErrBy)
	r.lastTick = now
	return p
}

// Bins mengembalikan histogram kumulatif sejauh ini (untuk histogram live).
func (r *Recorder) Bins(n int) []Bin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.all.Bins(n)
}

// Report membuat laporan akhir pada waktu end.
func (r *Recorder) Report(cfg Config, end time.Time) Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	el := end.Sub(r.start).Seconds()
	rep := Report{
		Config:       cfg,
		StartedAt:    r.start,
		ElapsedS:     el,
		Total:        r.ok + r.errs,
		OK:           r.ok,
		Errors:       r.errs,
		ErrorsByKind: maps.Clone(r.errBy),
		Timeline:     append([]Point(nil), r.timeline...),
		Histogram:    r.all.Bins(30),
		Latency: LatencySummary{
			Min:  ms(r.all.Min()),
			Mean: ms(r.all.Mean()),
			P50:  ms(r.all.Quantile(0.50)),
			P90:  ms(r.all.Quantile(0.90)),
			P95:  ms(r.all.Quantile(0.95)),
			P99:  ms(r.all.Quantile(0.99)),
			Max:  ms(r.all.Max()),
		},
	}
	if el > 0 {
		rep.RPS = float64(rep.Total) / el
	}
	if rep.Timeline == nil {
		rep.Timeline = []Point{}
	}
	return rep
}
