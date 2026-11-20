package main

import "testing"

func TestDecide(t *testing.T) {
	if ok, _ := decide("ok", 0, 0.5); !ok {
		t.Error("tanpa fault harus berhasil")
	}
	if ok, _ := decide("declined", 0, 0.9); ok {
		t.Error("kartu declined harus gagal")
	}
	if ok, _ := decide("ok", 1, 0.99); ok {
		t.Error("fail_rate 1 harus selalu gagal")
	}
	if ok, _ := decide("ok", 0.5, 0.7); !ok {
		t.Error("roll di atas fail_rate harus berhasil")
	}
}
