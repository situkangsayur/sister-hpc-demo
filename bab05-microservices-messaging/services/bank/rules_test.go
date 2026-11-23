package main

import (
	"errors"
	"testing"
	"time"
)

func TestApplySaldo(t *testing.T) {
	if s, err := applySaldo(1000, "setor", 500); err != nil || s != 1500 {
		t.Fatalf("setor: %d %v", s, err)
	}
	if s, err := applySaldo(1000, "bayar_belanja", 1000); err != nil || s != 0 {
		t.Fatalf("bayar pas: %d %v", s, err)
	}
	if _, err := applySaldo(1000, "transfer_keluar", 1001); !errors.Is(err, ErrSaldoKurang) {
		t.Fatalf("harus saldo kurang, dapat %v", err)
	}
	if _, err := applySaldo(1000, "hibah", 1); err == nil {
		t.Fatal("jenis tak dikenal harus ditolak")
	}
	if _, err := applySaldo(1000, "setor", 0); err == nil {
		t.Fatal("nominal 0 harus ditolak")
	}
}

func TestParseOccurred(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if got, _ := parseOccurred("", now); !got.Equal(now) {
		t.Fatal("kosong harus sekarang")
	}
	if got, err := parseOccurred("2026-11-01T08:00:00+07:00", now); err != nil || got.UTC().Day() != 1 {
		t.Fatalf("waktu lampau harus diterima: %v %v", got, err)
	}
	if _, err := parseOccurred("2026-11-24T10:00:00Z", now); err == nil {
		t.Fatal("masa depan harus ditolak")
	}
	if _, err := parseOccurred("kemarin", now); err == nil {
		t.Fatal("format salah harus ditolak")
	}
}

func TestNextAkad(t *testing.T) {
	ok := [][3]string{{"diajukan", "setujui", "disetujui"}, {"disetujui", "cairkan", "cair"},
		{"diajukan", "tolak", "ditolak"}, {"cair", "angsuran", "cair"}}
	for _, c := range ok {
		if got, err := nextAkad(c[0], c[1]); err != nil || got != c[2] {
			t.Errorf("%s --%s--> %q (%v), seharusnya %s", c[0], c[1], got, err, c[2])
		}
	}
	for _, c := range [][2]string{{"diajukan", "cairkan"}, {"lunas", "angsuran"}, {"cair", "tolak"}, {"ditolak", "setujui"}} {
		if _, err := nextAkad(c[0], c[1]); err == nil {
			t.Errorf("%s --%s--> harus ditolak", c[0], c[1])
		}
	}
}

func TestAngsuranAkad(t *testing.T) {
	// pokok 1.000.000 + margin 200.000, tenor 12 -> 100.000 per bulan
	if n, l := angsuranAkad(1_000_000, 200_000, 12, 0, 0); n != 100_000 || l {
		t.Fatalf("angsuran pertama %d lunas=%v", n, l)
	}
	if n, l := angsuranAkad(1_000_000, 200_000, 12, 1_150_000, 0); n != 50_000 || !l {
		t.Fatalf("angsuran terakhir dipotong ke sisa: %d lunas=%v", n, l)
	}
	if n, l := angsuranAkad(1_000, 0, 3, 0, 0); n != 334 || l {
		t.Fatalf("pembulatan ke atas: %d %v", n, l)
	}
}
