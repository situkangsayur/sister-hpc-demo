package main

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Status node menurut failure detector sederhana berbasis heartbeat.
const (
	StatusHidup     = "hidup"     // heartbeat dijawab cepat
	StatusLambat    = "lambat"    // heartbeat dijawab, tetapi RTT di atas ambang
	StatusDicurigai = "dicurigai" // heartbeat terakhir gagal, belum cukup bukti untuk menyatakan mati
	StatusMati      = "mati"      // beberapa heartbeat berturut-turut gagal
)

const (
	AmbangLambat       = 150 * time.Millisecond
	BatasGagalBeruntun = 3
)

// Klasifikasi menentukan status dari hasil heartbeat terakhir dan jumlah gagal berturut-turut.
// Perhatikan: monitor tidak pernah tahu pasti node mati; ia hanya tahu jawabannya tidak datang.
func Klasifikasi(ok bool, rtt time.Duration, gagalBeruntun int) string {
	if ok {
		if rtt >= AmbangLambat {
			return StatusLambat
		}
		return StatusHidup
	}
	if gagalBeruntun >= BatasGagalBeruntun {
		return StatusMati
	}
	return StatusDicurigai
}

// Persentil mengembalikan persentil p (0..100) dari sampel (metode nearest-rank).
func Persentil(sampel []float64, p float64) float64 {
	if len(sampel) == 0 {
		return 0
	}
	s := append([]float64(nil), sampel...)
	sort.Float64s(s)
	idx := int(math.Ceil(p/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// AvailabilityParalel = 1 - prod(1 - a_i): sistem hidup selama minimal satu replica hidup.
func AvailabilityParalel(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	mati := 1.0
	for _, x := range a {
		mati *= 1 - x
	}
	return 1 - mati
}

// Hasil satu request dari generator lalu lintas.
type Hasil struct {
	Node    string  // node yang menjawab ("" bila gagal)
	OK      bool    // status 2xx
	Jenis   string  // jenis kegagalan bila !OK
	Retry   bool    // nginx mencoba lebih dari satu node
	Latency float64 // ms
	Tulis   bool
}

// Bucket mengumpulkan hasil dalam satu detik.
type Bucket struct {
	T       int64          `json:"t"` // detik unix
	PerNode map[string]int `json:"per_node"`
	Sukses  int            `json:"sukses"`
	Gagal   int            `json:"gagal"`
	Retry   int            `json:"retry"`
	P50     float64        `json:"p50"`
	P95     float64        `json:"p95"`
	Maks    float64        `json:"maks"`
	lat     []float64
}

// Kumulatif menghitung total sejak reset.
type Kumulatif struct {
	Kirim       int            `json:"kirim"`
	Sukses      int            `json:"sukses"`
	Gagal       int            `json:"gagal"`
	Retry       int            `json:"retry"`
	PerNode     map[string]int `json:"per_node"`
	GagalJenis  map[string]int `json:"gagal_jenis"`
	TulisSukses int            `json:"tulis_sukses"`
	TulisGagal  int            `json:"tulis_gagal"`
}

// Seri adalah ring buffer bucket per detik.
type Seri struct {
	mu      sync.Mutex
	panjang int
	bucket  []*Bucket
	kum     Kumulatif
}

func NewSeri(panjang int) *Seri {
	s := &Seri{panjang: panjang}
	s.Reset()
	return s
}

func (s *Seri) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bucket = nil
	s.kum = Kumulatif{PerNode: map[string]int{}, GagalJenis: map[string]int{}}
}

func (s *Seri) ambil(t int64) *Bucket {
	if n := len(s.bucket); n > 0 && s.bucket[n-1].T == t {
		return s.bucket[n-1]
	}
	b := &Bucket{T: t, PerNode: map[string]int{}}
	s.bucket = append(s.bucket, b)
	if len(s.bucket) > s.panjang {
		s.bucket = s.bucket[len(s.bucket)-s.panjang:]
	}
	return b
}

// Tambah mencatat satu hasil pada detik t.
func (s *Seri) Tambah(t time.Time, h Hasil) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.ambil(t.Unix())
	s.kum.Kirim++
	if h.Retry {
		b.Retry++
		s.kum.Retry++
	}
	if h.OK {
		b.Sukses++
		b.PerNode[h.Node]++
		b.lat = append(b.lat, h.Latency)
		s.kum.Sukses++
		s.kum.PerNode[h.Node]++
		if h.Tulis {
			s.kum.TulisSukses++
		}
	} else {
		b.Gagal++
		s.kum.Gagal++
		s.kum.GagalJenis[h.Jenis]++
		if h.Tulis {
			s.kum.TulisGagal++
		}
	}
}

// Snapshot menyalin bucket (dengan persentil terhitung) dan kumulatif.
func (s *Seri) Snapshot() ([]Bucket, Kumulatif) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bucket, 0, len(s.bucket))
	for _, b := range s.bucket {
		c := *b
		c.PerNode = salinMap(b.PerNode)
		c.P50, c.P95, c.Maks = Persentil(b.lat, 50), Persentil(b.lat, 95), Persentil(b.lat, 100)
		c.lat = nil
		out = append(out, c)
	}
	k := s.kum
	k.PerNode = salinMap(s.kum.PerNode)
	k.GagalJenis = salinMap(s.kum.GagalJenis)
	return out, k
}

func salinMap(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Jendela menyimpan n hasil heartbeat terakhir untuk menghitung availability terukur.
type Jendela struct {
	buf []bool
	n   int
}

func (j *Jendela) Tambah(ok bool) {
	j.buf = append(j.buf, ok)
	if len(j.buf) > j.n {
		j.buf = j.buf[len(j.buf)-j.n:]
	}
}

func (j *Jendela) Rasio() float64 {
	if len(j.buf) == 0 {
		return 0
	}
	ok := 0
	for _, b := range j.buf {
		if b {
			ok++
		}
	}
	return float64(ok) / float64(len(j.buf))
}
