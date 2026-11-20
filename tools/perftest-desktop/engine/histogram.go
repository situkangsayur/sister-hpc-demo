package engine

import (
	"math"
	"time"
)

// Histogram latency dengan bucket logaritmik (lebar relatif 1%), mirip HdrHistogram versi
// sederhana. Memori tetap (~15 KB) berapa pun jumlah sampel, dan kuantil (p50/p95/p99)
// akurat sampai sekitar 1%. Rentang 1 µs sampai 120 detik; nilai di luar itu dijepit.
// Histogram tidak aman dipakai bersamaan; Recorder yang mengunci.
type Histogram struct {
	counts []uint64
	total  uint64
	sumUs  float64
	min    time.Duration
	max    time.Duration
}

const (
	histGrowth = 1.01
	histMaxUs  = 120e6
)

var (
	histLogBase = math.Log(histGrowth)
	histBuckets = int(math.Ceil(math.Log(histMaxUs)/histLogBase)) + 2
)

// NewHistogram membuat histogram kosong.
func NewHistogram() *Histogram {
	return &Histogram{counts: make([]uint64, histBuckets)}
}

// bucket i>=1 mencakup [g^(i-1), g^i) mikrodetik; bucket 0 untuk < 1 µs.
func bucketOf(us float64) int {
	if us < 1 {
		return 0
	}
	i := int(math.Log(us)/histLogBase) + 1
	if i >= histBuckets {
		i = histBuckets - 1
	}
	return i
}

func bucketLowUs(i int) float64 {
	if i <= 0 {
		return 0
	}
	return math.Pow(histGrowth, float64(i-1))
}

func bucketMidUs(i int) float64 {
	if i <= 0 {
		return 0.5
	}
	lo := bucketLowUs(i)
	return lo + lo*(histGrowth-1)/2
}

// Record menambah satu sampel.
func (h *Histogram) Record(d time.Duration) {
	if d < 0 {
		d = 0
	}
	us := float64(d) / float64(time.Microsecond)
	h.counts[bucketOf(us)]++
	if h.total == 0 || d < h.min {
		h.min = d
	}
	if d > h.max {
		h.max = d
	}
	h.total++
	h.sumUs += us
}

// Count adalah jumlah sampel.
func (h *Histogram) Count() uint64 { return h.total }

// Min dan Max adalah nilai ekstrem yang tepat (bukan perkiraan bucket).
func (h *Histogram) Min() time.Duration { return h.min }

// Max lihat Min.
func (h *Histogram) Max() time.Duration { return h.max }

// Mean adalah rata-rata tepat.
func (h *Histogram) Mean() time.Duration {
	if h.total == 0 {
		return 0
	}
	return time.Duration(h.sumUs / float64(h.total) * float64(time.Microsecond))
}

// Quantile mengembalikan perkiraan kuantil q (0..1), dijepit ke [Min, Max].
func (h *Histogram) Quantile(q float64) time.Duration {
	if h.total == 0 {
		return 0
	}
	if q <= 0 {
		return h.min
	}
	if q >= 1 {
		return h.max
	}
	rank := uint64(math.Ceil(q * float64(h.total)))
	if rank == 0 {
		rank = 1
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		if cum >= rank {
			if i == histBuckets-1 {
				return h.max // bucket luapan (> 120 s)
			}
			d := time.Duration(bucketMidUs(i) * float64(time.Microsecond))
			if d < h.min {
				d = h.min
			}
			if d > h.max {
				d = h.max
			}
			return d
		}
	}
	return h.max
}

// Merge menambahkan isi o ke h.
func (h *Histogram) Merge(o *Histogram) {
	if o.total == 0 {
		return
	}
	for i, c := range o.counts {
		h.counts[i] += c
	}
	if h.total == 0 || o.min < h.min {
		h.min = o.min
	}
	if o.max > h.max {
		h.max = o.max
	}
	h.total += o.total
	h.sumUs += o.sumUs
}

// Reset mengosongkan histogram.
func (h *Histogram) Reset() {
	clear(h.counts)
	h.total, h.sumUs, h.min, h.max = 0, 0, 0, 0
}

// Bin adalah satu batang histogram untuk ditampilkan (milidetik).
type Bin struct {
	FromMs float64 `json:"fromMs"`
	ToMs   float64 `json:"toMs"`
	Count  uint64  `json:"count"`
}

// Bins meringkas histogram menjadi n batang linear dari Min sampai p99.9; sampel di atas
// p99.9 masuk batang terakhir supaya ekor panjang tidak membuat grafik gepeng.
func (h *Histogram) Bins(n int) []Bin {
	if h.total == 0 || n <= 0 {
		return []Bin{}
	}
	lo := float64(h.min) / float64(time.Millisecond)
	hi := float64(h.Quantile(0.999)) / float64(time.Millisecond)
	if hi <= lo {
		hi = lo + 0.001
	}
	w := (hi - lo) / float64(n)
	bins := make([]Bin, n)
	for i := range bins {
		bins[i].FromMs = lo + float64(i)*w
		bins[i].ToMs = lo + float64(i+1)*w
	}
	bins[n-1].ToMs = float64(h.max) / float64(time.Millisecond)
	for i, c := range h.counts {
		if c == 0 {
			continue
		}
		ms := bucketMidUs(i) / 1000
		j := int((ms - lo) / w)
		if j < 0 {
			j = 0
		}
		if j >= n {
			j = n - 1
		}
		bins[j].Count += c
	}
	return bins
}
