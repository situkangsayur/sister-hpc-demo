package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Gerbang CI: konstanta Pesan yang akan di-deploy harus valid.
func TestPesanValid(t *testing.T) {
	if err := validasiPesan(Pesan); err != nil {
		t.Fatalf("Pesan tidak valid: %v", err)
	}
}

func TestValidasiPesan(t *testing.T) {
	kasus := []struct {
		pesan string
		ok    bool
	}{
		{"halo", true},
		{"", false},
		{"   ", false},
		{strings.Repeat("a", PanjangMaksPesan), true},
		{strings.Repeat("a", PanjangMaksPesan+1), false},
	}
	for _, k := range kasus {
		if err := validasiPesan(k.pesan); (err == nil) != k.ok {
			t.Errorf("validasiPesan(%q) err=%v, ingin ok=%v", k.pesan, err, k.ok)
		}
	}
}

func TestHandlerRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	handler("pod-uji").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var j Jawaban
	if err := json.NewDecoder(rec.Body).Decode(&j); err != nil {
		t.Fatal(err)
	}
	if j.Pod != "pod-uji" || j.Pesan != Pesan {
		t.Fatalf("jawaban tidak sesuai: %+v", j)
	}
}
