package main

import (
	"math"
	"testing"
	"time"
)

func TestKlasifikasi(t *testing.T) {
	kasus := []struct {
		ok    bool
		rtt   time.Duration
		gagal int
		mau   string
	}{
		{true, 2 * time.Millisecond, 0, StatusHidup},
		{true, 400 * time.Millisecond, 0, StatusLambat},
		{false, 0, 1, StatusDicurigai},
		{false, 0, 2, StatusDicurigai},
		{false, 0, 3, StatusMati},
	}
	for _, k := range kasus {
		if got := Klasifikasi(k.ok, k.rtt, k.gagal); got != k.mau {
			t.Errorf("Klasifikasi(%v,%v,%d)=%s, mau %s", k.ok, k.rtt, k.gagal, got, k.mau)
		}
	}
}

func TestPersentil(t *testing.T) {
	s := []float64{5, 1, 4, 2, 3, 6, 7, 8, 9, 10}
	if p := Persentil(s, 50); p != 5 {
		t.Errorf("p50=%v", p)
	}
	if p := Persentil(s, 95); p != 10 {
		t.Errorf("p95=%v", p)
	}
	if p := Persentil(nil, 50); p != 0 {
		t.Errorf("kosong=%v", p)
	}
}

func TestAvailabilityParalel(t *testing.T) {
	// Contoh buku: tiga replica 99,9% -> 1 - 1e-9
	got := AvailabilityParalel([]float64{0.999, 0.999, 0.999})
	if math.Abs(got-(1-1e-9)) > 1e-12 {
		t.Errorf("got %v", got)
	}
	if AvailabilityParalel([]float64{0.99, 0.99}) < 0.9998 {
		t.Error("dua server 99% harus 99,99%")
	}
}

func TestSeri(t *testing.T) {
	s := NewSeri(3)
	t0 := time.Unix(1000, 0)
	s.Tambah(t0, Hasil{Node: "node1", OK: true, Latency: 10})
	s.Tambah(t0, Hasil{Node: "node2", OK: true, Latency: 30, Retry: true})
	s.Tambah(t0, Hasil{OK: false, Jenis: "timeout", Tulis: true})
	for i := 1; i <= 4; i++ {
		s.Tambah(t0.Add(time.Duration(i)*time.Second), Hasil{Node: "node3", OK: true, Latency: 1})
	}
	b, k := s.Snapshot()
	if len(b) != 3 || b[0].T != 1002 {
		t.Fatalf("ring buffer salah: %d bucket, awal %d", len(b), b[0].T)
	}
	if k.Kirim != 7 || k.Sukses != 6 || k.Gagal != 1 || k.Retry != 1 || k.TulisGagal != 1 || k.GagalJenis["timeout"] != 1 {
		t.Errorf("kumulatif salah: %+v", k)
	}
}

func TestJendela(t *testing.T) {
	j := &Jendela{n: 4}
	for _, ok := range []bool{false, true, true, true, false} {
		j.Tambah(ok)
	}
	if r := j.Rasio(); r != 0.75 {
		t.Errorf("rasio=%v", r)
	}
}
