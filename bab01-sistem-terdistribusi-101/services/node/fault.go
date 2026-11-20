package main

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// Mode kegagalan sebuah node. Ketiganya dari sisi klien bisa tampak sama: tidak ada jawaban.
const (
	ModeNormal = "normal" // melayani seperti biasa (latency/drop tetap berlaku bila diisi)
	ModeHang   = "hang"   // menerima koneksi tetapi tidak pernah menjawab (klien menunggu sampai timeout)
	ModeMati   = "mati"   // port utama ditutup, koneksi ditolak (connection refused)
)

// Fault adalah gangguan yang sedang disuntikkan ke node lewat endpoint admin.
type Fault struct {
	Mode            string `json:"mode"`
	LatencyMs       int    `json:"latency_ms"`        // tambahan latency tetap per request
	JitterMs        int    `json:"jitter_ms"`         // tambahan acak 0..jitter
	DropRequestPct  int    `json:"drop_request_pct"`  // persen request diputus sebelum diproses
	DropResponsePct int    `json:"drop_response_pct"` // persen request diproses tetapi jawabannya diputus
	SkewMs          int    `json:"skew_ms"`           // pergeseran jam node (clock skew)
}

// Validate memeriksa batas nilai agar demo tidak bisa dibuat menggantung tanpa batas.
func (f Fault) Validate() error {
	switch f.Mode {
	case ModeNormal, ModeHang, ModeMati:
	default:
		return errors.New("mode harus normal, hang, atau mati")
	}
	if f.LatencyMs < 0 || f.LatencyMs > 10000 || f.JitterMs < 0 || f.JitterMs > 10000 {
		return errors.New("latency_ms dan jitter_ms harus 0..10000")
	}
	if f.DropRequestPct < 0 || f.DropRequestPct > 100 || f.DropResponsePct < 0 || f.DropResponsePct > 100 {
		return errors.New("persen drop harus 0..100")
	}
	if f.SkewMs < -600000 || f.SkewMs > 600000 {
		return errors.New("skew_ms harus -600000..600000")
	}
	return nil
}

// Keputusan untuk satu request, dihitung sekali di awal request.
type Decision struct {
	Hang         bool
	DropRequest  bool
	DropResponse bool
	Delay        time.Duration
}

// Decide menerjemahkan fault menjadi keputusan konkret memakai sumber acak r (0..1).
func (f Fault) Decide(r func() float64) Decision {
	d := Decision{}
	if f.Mode == ModeHang {
		d.Hang = true
		return d
	}
	d.Delay = time.Duration(f.LatencyMs) * time.Millisecond
	if f.JitterMs > 0 {
		d.Delay += time.Duration(r()*float64(f.JitterMs)) * time.Millisecond
	}
	if f.DropRequestPct > 0 && r()*100 < float64(f.DropRequestPct) {
		d.DropRequest = true
		return d
	}
	if f.DropResponsePct > 0 && r()*100 < float64(f.DropResponsePct) {
		d.DropResponse = true
	}
	return d
}

// FaultBox menyimpan fault saat ini secara aman untuk banyak goroutine.
type FaultBox struct {
	mu sync.RWMutex
	f  Fault
}

func NewFaultBox() *FaultBox { return &FaultBox{f: Fault{Mode: ModeNormal}} }

func (b *FaultBox) Get() Fault {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.f
}

func (b *FaultBox) Set(f Fault) {
	b.mu.Lock()
	b.f = f
	b.mu.Unlock()
}

// Now mengembalikan "jam node", yaitu jam sistem ditambah skew yang disuntikkan.
func (b *FaultBox) Now() time.Time {
	return time.Now().Add(time.Duration(b.Get().SkewMs) * time.Millisecond)
}

func randFloat() float64 { return rand.Float64() }
