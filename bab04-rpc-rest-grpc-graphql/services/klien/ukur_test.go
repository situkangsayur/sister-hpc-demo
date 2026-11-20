package main

import (
	"testing"

	"google.golang.org/protobuf/proto"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/rest"
	"sister/bab04/internal/toko"
)

// Contoh di slide P04: produk 42 Kopi 25000 = JSON 37 byte, Protobuf 12 byte.
func TestUkuranSlide(t *testing.T) {
	p := toko.Produk{ID: 42, Nama: "Kopi", Harga: 25000}
	if n := len(rest.KodeJSON(p)); n != 37 {
		t.Fatalf("JSON %d byte, harus 37", n)
	}
	b, err := proto.Marshal(&pb.Produk{Id: 42, Nama: "Kopi", Harga: 25000})
	if err != nil {
		t.Fatal(err)
	}
	if got := hexSpasi(b); got != "08 2A 12 04 4B 6F 70 69 18 A8 C3 01" {
		t.Fatalf("protobuf %s", got)
	}
	batas := 100
	a := anotasiProto(b, 0, &batas)
	if len(a) != 3 || a[0].Keterangan != "field 1, varint = 42" || a[1].Keterangan != `field 2, panjang 4, "Kopi"` {
		t.Fatalf("anotasi %+v", a)
	}
}

func TestPersentil(t *testing.T) {
	s := hitungStatistik([]float64{5, 1, 4, 2, 3, 100, 6, 7, 8, 9})
	if s.P50 != 5 || s.P99 != 100 || s.Min != 1 || s.Maks != 100 {
		t.Fatalf("statistik %+v", s)
	}
	if s.Rata <= s.P50 {
		t.Fatal("rata-rata seharusnya tertarik pencilan")
	}
}

func TestBackoff(t *testing.T) {
	for i, want := range []int{100, 200, 400, 800} {
		if got := backoff(100, i, false); got != want {
			t.Fatalf("percobaan %d: %d, harus %d", i, got, want)
		}
	}
	for i := 0; i < 50; i++ {
		if got := backoff(100, 3, true); got < 0 || got > 800 {
			t.Fatalf("full jitter di luar [0,800]: %d", got)
		}
	}
	if got := backoff(1000, 10, false); got != 5000 {
		t.Fatalf("batas atas backoff: %d", got)
	}
}
