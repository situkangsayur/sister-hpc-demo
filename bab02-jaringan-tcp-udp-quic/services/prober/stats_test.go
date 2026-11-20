package main

import "testing"

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{5, 1, 3, 2, 4})
	if s.Min != 1 || s.Max != 5 || s.Median != 3 || s.Mean != 3 || s.Count != 5 {
		t.Fatalf("%+v", s)
	}
	if s.P95 != 4.8 {
		t.Fatalf("p95 = %v", s.P95)
	}
	if (Summarize(nil) != Stats{}) {
		t.Fatal("kosong harus nol")
	}
}

func TestStallInfo(t *testing.T) {
	// stream 0 dan 1 tersendat bersamaan di t=100 (gaya TCP), stream 2 sendiri di t=300
	var xs []HOLSample
	for i := 0; i < 30; i++ {
		for s := 0; s < 3; s++ {
			xs = append(xs, HOLSample{Stream: s, Seq: i, T: float64(i * 20), Lat: 10})
		}
	}
	xs[5*3+0].Lat, xs[5*3+1].Lat = 200, 210 // t=100
	xs[15*3+2].Lat = 300                    // t=300
	n, pct, _ := stallInfo(xs, 20)
	if n != 3 {
		t.Fatalf("stalls = %d", n)
	}
	if pct < 66 || pct > 67 {
		t.Fatalf("shared pct = %v", pct)
	}
}
