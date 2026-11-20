package main

import (
	"testing"
	"time"
)

func tetap(v float64) func() float64 { return func() float64 { return v } }

func TestValidate(t *testing.T) {
	kasus := []struct {
		f  Fault
		ok bool
	}{
		{Fault{Mode: ModeNormal}, true},
		{Fault{Mode: ModeHang}, true},
		{Fault{Mode: ModeMati, LatencyMs: 500}, true},
		{Fault{Mode: "rusak"}, false},
		{Fault{Mode: ModeNormal, DropRequestPct: 101}, false},
		{Fault{Mode: ModeNormal, LatencyMs: -1}, false},
		{Fault{Mode: ModeNormal, SkewMs: 700000}, false},
	}
	for _, k := range kasus {
		if err := k.f.Validate(); (err == nil) != k.ok {
			t.Errorf("%+v: ok=%v, err=%v", k.f, k.ok, err)
		}
	}
}

func TestDecide(t *testing.T) {
	if d := (Fault{Mode: ModeHang, LatencyMs: 100}).Decide(tetap(0)); !d.Hang || d.Delay != 0 {
		t.Errorf("hang: %+v", d)
	}
	d := Fault{Mode: ModeNormal, LatencyMs: 100, JitterMs: 50}.Decide(tetap(0.5))
	if d.Delay != 125*time.Millisecond {
		t.Errorf("delay = %v, mau 125ms", d.Delay)
	}
	// r=0.3 -> 30 < 50 berarti request diputus, response tidak perlu dinilai lagi
	d = Fault{Mode: ModeNormal, DropRequestPct: 50, DropResponsePct: 100}.Decide(tetap(0.3))
	if !d.DropRequest || d.DropResponse {
		t.Errorf("drop request: %+v", d)
	}
	d = Fault{Mode: ModeNormal, DropRequestPct: 20, DropResponsePct: 50}.Decide(tetap(0.3))
	if d.DropRequest || !d.DropResponse {
		t.Errorf("drop response: %+v", d)
	}
	if d := (Fault{Mode: ModeNormal}).Decide(tetap(0)); d.DropRequest || d.DropResponse || d.Delay != 0 {
		t.Errorf("normal: %+v", d)
	}
}

func TestKerjaDeterministik(t *testing.T) {
	if Kerja(7) != Kerja(7) || Kerja(7) == Kerja(8) {
		t.Error("Kerja harus deterministik dan bergantung pada x")
	}
}
