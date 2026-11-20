package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "sister/bab04/gen/katalogv1"
)

// backoff menghitung jeda sebelum percobaan berikutnya: exponential backoff
// (dasar * 2^percobaan, dibatasi 5 detik), dengan full jitter bila diminta,
// yaitu acak seragam di [0, nilai itu] agar banyak klien tidak retry serentak.
func backoff(dasarMs, percobaan int, jitter bool) int {
	d := dasarMs << percobaan
	if d > 5000 || d <= 0 {
		d = 5000
	}
	if jitter {
		return rand.IntN(d + 1)
	}
	return d
}

func kunciAcak() string {
	b := make([]byte, 6)
	if _, err := cryptoRead(b); err != nil {
		return fmt.Sprintf("k%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

var cryptoRead = crand.Read

// Percobaan adalah satu attempt pada timeline retry.
type Percobaan struct {
	Ke        int     `json:"ke"`
	MulaiMs   float64 `json:"mulai_ms"`
	DurasiMs  float64 `json:"durasi_ms"`
	Hasil     string  `json:"hasil"` // ok, timeout, gagal, ditolak
	Status    string  `json:"status"`
	DariCache bool    `json:"dari_cache"`
	TungguMs  int     `json:"tunggu_ms"` // jeda backoff sesudah percobaan ini
}

type saldoInfo struct {
	Saldo            int64 `json:"saldo"`
	JumlahPembayaran int64 `json:"jumlah_pembayaran"`
}

func (k *Klien) bacaSaldo(ctx context.Context, akun string) (saldoInfo, error) {
	var s saldoInfo
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, k.gwHTTP+"/v1/saldo/"+akun, nil)
	if err != nil {
		return s, err
	}
	resp, err := k.http.Do(r)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// bayarREST: satu percobaan POST /v1/bayar dengan timeout.
func (k *Klien) bayarREST(ctx context.Context, kunci, akun string, jumlah int64) (string, string, bool) {
	isi, _ := json.Marshal(map[string]any{"akun": akun, "jumlah": jumlah})
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, k.gwHTTP+"/v1/bayar", bytes.NewReader(isi))
	r.Header.Set("Content-Type", "application/json")
	if kunci != "" {
		r.Header.Set("Idempotency-Key", kunci)
	}
	resp, err := k.http.Do(r)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "timeout", "client timeout (context deadline exceeded)", false
		}
		return "gagal", err.Error(), false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	cache := resp.Header.Get("Idempotent-Replayed") == "true"
	switch {
	case resp.StatusCode < 300:
		return "ok", resp.Status, cache
	case resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500:
		return "gagal", resp.Status, false // layak di-retry
	default:
		return "ditolak", resp.Status, false // 4xx lain: retry tidak menolong
	}
}

// bayarGRPC: satu percobaan Bayar dengan deadline; kunci lewat metadata.
func (k *Klien) bayarGRPC(ctx context.Context, kunci, akun string, jumlah int64) (string, string, bool) {
	if kunci != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "idempotency-key", kunci)
	}
	resp, err := k.gwGRPC.Bayar(ctx, &pb.BayarRequest{Akun: akun, Jumlah: jumlah})
	if err == nil {
		return "ok", "OK", resp.GetDariCache()
	}
	st := status.Convert(err)
	switch st.Code() {
	case codes.DeadlineExceeded:
		return "timeout", "DEADLINE_EXCEEDED", false
	case codes.Unavailable, codes.Aborted, codes.ResourceExhausted:
		return "gagal", st.Code().String() + ": " + st.Message(), false
	default:
		return "ditolak", st.Code().String() + ": " + st.Message(), false
	}
}

func (k *Klien) andal(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Protokol    string `json:"protokol"`
		TimeoutMs   int    `json:"timeout_ms"`
		Maks        int    `json:"maks_percobaan"`
		BackoffMs   int    `json:"backoff_ms"`
		Jitter      bool   `json:"jitter"`
		Idempotency bool   `json:"idempotency"`
		Akun        string `json:"akun"`
		Jumlah      int64  `json:"jumlah"`
		LambatMs    *int   `json:"lambat_ms"`
		LambatKali  int    `json:"lambat_kali"`
		GagalPersen *int   `json:"gagal_persen"`
	}
	if bacaJSON(r, &q) != nil {
		tulisJSON(w, http.StatusBadRequest, map[string]string{"error": "json tidak sah"})
		return
	}
	if q.TimeoutMs <= 0 {
		q.TimeoutMs = 500
	}
	if q.Maks <= 0 || q.Maks > 8 {
		q.Maks = 4
	}
	if q.BackoffMs < 0 {
		q.BackoffMs = 100
	}
	if q.Akun == "" {
		q.Akun = "budi"
	}
	if q.Jumlah <= 0 {
		q.Jumlah = 25000
	}
	ctx := r.Context()
	if q.LambatMs != nil && q.GagalPersen != nil {
		if _, err := k.kirimGangguan(ctx, *q.LambatMs, q.LambatKali, *q.GagalPersen); err != nil {
			tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	sebelum, err := k.bacaSaldo(ctx, q.Akun)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": "membaca saldo: " + err.Error()})
		return
	}
	kunci := ""
	if q.Idempotency {
		kunci = kunciAcak() // satu kunci untuk SATU operasi bisnis, dipakai ulang di setiap retry
	}
	mulai := time.Now()
	var log []Percobaan
	berhasil := false
	for i := 0; i < q.Maks; i++ {
		c, batal := context.WithTimeout(ctx, time.Duration(q.TimeoutMs)*time.Millisecond)
		t0 := time.Now()
		var hasil, st string
		var cache bool
		if q.Protokol == "grpc" {
			hasil, st, cache = k.bayarGRPC(c, kunci, q.Akun, q.Jumlah)
		} else {
			hasil, st, cache = k.bayarREST(c, kunci, q.Akun, q.Jumlah)
		}
		batal()
		p := Percobaan{Ke: i + 1, MulaiMs: float64(t0.Sub(mulai).Microseconds()) / 1000, DurasiMs: msSejak(t0), Hasil: hasil, Status: st, DariCache: cache}
		if hasil == "ok" {
			berhasil = true
			log = append(log, p)
			break
		}
		if hasil == "ditolak" || i == q.Maks-1 {
			log = append(log, p)
			break
		}
		p.TungguMs = backoff(q.BackoffMs, i, q.Jitter)
		log = append(log, p)
		select {
		case <-time.After(time.Duration(p.TungguMs) * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
	total := msSejak(mulai)
	time.Sleep(150 * time.Millisecond) // beri waktu commit terakhir di server
	sesudah, _ := k.bacaSaldo(ctx, q.Akun)
	dipotong := sesudah.JumlahPembayaran - sebelum.JumlahPembayaran
	tulisJSON(w, http.StatusOK, map[string]any{
		"protokol": map[bool]string{true: "grpc", false: "rest"}[q.Protokol == "grpc"],
		"kunci":    kunci, "berhasil": berhasil, "percobaan": log, "total_ms": total,
		"saldo_sebelum": sebelum.Saldo, "saldo_sesudah": sesudah.Saldo,
		"dipotong_kali": dipotong, "jumlah": q.Jumlah,
	})
}

// kirimGangguan mengatur fault injection lewat endpoint admin replica pertama.
// Endpoint /admin tidak dibuka di APISIX; pengaturannya disimpan di Postgres
// sehingga berlaku untuk semua replica.
func (k *Klien) kirimGangguan(ctx context.Context, lambat, kali, gagal int) (json.RawMessage, error) {
	isi, _ := json.Marshal(map[string]int{"lambat_ms": lambat, "lambat_kali": kali, "gagal_persen": gagal})
	r, _ := http.NewRequestWithContext(ctx, http.MethodPut, k.nodes[0]+"/admin/gangguan", bytes.NewReader(isi))
	resp, err := k.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("mengatur gangguan: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, err
}

func (k *Klien) ambilGangguan(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, k.nodes[0]+"/admin/gangguan", nil)
	resp, err := k.http.Do(req)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func (k *Klien) setGangguan(w http.ResponseWriter, r *http.Request) {
	var q struct {
		LambatMs    int `json:"lambat_ms"`
		LambatKali  int `json:"lambat_kali"`
		GagalPersen int `json:"gagal_persen"`
	}
	if bacaJSON(r, &q) != nil {
		tulisJSON(w, http.StatusBadRequest, map[string]string{"error": "json tidak sah"})
		return
	}
	hasil, err := k.kirimGangguan(r.Context(), q.LambatMs, q.LambatKali, q.GagalPersen)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	tulisJSON(w, http.StatusOK, hasil)
}

func (k *Klien) saldo(w http.ResponseWriter, r *http.Request) {
	s, err := k.bacaSaldo(r.Context(), "budi")
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	tulisJSON(w, http.StatusOK, s)
}

func (k *Klien) resetSaldo(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, k.gwHTTP+"/v1/saldo/budi/reset", nil)
	resp, err := k.http.Do(req)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	resp.Body.Close()
	k.saldo(w, r)
}
