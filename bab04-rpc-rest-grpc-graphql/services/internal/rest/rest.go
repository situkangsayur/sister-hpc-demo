// Paket rest adalah adapter REST untuk toko. Satu resource dilayani dalam dua
// representasi: JSON (bawaan) dan XML (Accept: application/xml), sesuai prinsip
// REST Fielding bahwa resource terpisah dari representasinya. Error memakai
// format Problem Details RFC 9457.
package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sister/bab04/internal/gangguan"
	"sister/bab04/internal/toko"
)

// Server memegang dependensi handler REST.
type Server struct {
	Toko     *toko.Toko
	Gangguan *gangguan.Penjaga
	Node     string
}

// Daftarkan memasang semua route REST ke mux.
func (s *Server) Daftarkan(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/produk", s.daftar)
	mux.HandleFunc("GET /v1/produk/{id}", s.ambil)
	mux.HandleFunc("PUT /v1/produk/{id}/harga", s.ubahHarga)
	mux.HandleFunc("POST /v1/bayar", s.bayar)
	mux.HandleFunc("GET /v1/saldo/{akun}", s.saldo)
	mux.HandleFunc("POST /v1/saldo/{akun}/reset", s.resetSaldo)
	mux.HandleFunc("GET /admin/gangguan", s.ambilGangguan)
	mux.HandleFunc("PUT /admin/gangguan", s.setGangguan)
}

// mauXML memeriksa content negotiation: header Accept atau ?format=xml.
func mauXML(r *http.Request) bool {
	return r.URL.Query().Get("format") == "xml" || strings.Contains(r.Header.Get("Accept"), "application/xml")
}

// Masalah adalah Problem Details (RFC 9457).
type Masalah struct {
	XMLName  xml.Name `json:"-" xml:"problem"`
	Type     string   `json:"type" xml:"type"`
	Title    string   `json:"title" xml:"title"`
	Status   int      `json:"status" xml:"status"`
	Detail   string   `json:"detail,omitempty" xml:"detail,omitempty"`
	Instance string   `json:"instance" xml:"instance"`
}

func tulisMasalah(w http.ResponseWriter, r *http.Request, status int, judul, detail string) {
	m := Masalah{Type: "about:blank", Title: judul, Status: status, Detail: detail, Instance: r.URL.Path}
	if mauXML(r) {
		w.Header().Set("Content-Type", "application/problem+xml")
		w.WriteHeader(status)
		b, _ := xml.Marshal(m)
		w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	b, _ := json.Marshal(m)
	w.Write(b)
}

// produkXML dan daftarXML adalah bentuk XML resource produk.
type produkXML struct {
	XMLName xml.Name `xml:"produk"`
	toko.Produk
}

type daftarXML struct {
	XMLName xml.Name      `xml:"daftarProduk"`
	Produk  []toko.Produk `xml:"produk"`
}

// KodeJSON dan KodeXML diekspor agar uji unit dapat memeriksa ukuran byte.
func KodeJSON(v any) []byte {
	b, _ := json.Marshal(v) // tanpa newline: {"id":42,"nama":"Kopi","harga":25000} = 37 byte
	return b
}

func KodeXML(v any) []byte {
	switch x := v.(type) {
	case toko.Produk:
		v = produkXML{Produk: x}
	case []toko.Produk:
		v = daftarXML{Produk: x}
	}
	b, _ := xml.Marshal(v)
	return b
}

// tulis mengirim representasi JSON atau XML, dengan ETag untuk GET.
func tulis(w http.ResponseWriter, r *http.Request, status int, v any) {
	var b []byte
	if mauXML(r) {
		w.Header().Set("Content-Type", "application/xml")
		b = KodeXML(v)
	} else {
		w.Header().Set("Content-Type", "application/json")
		b = KodeJSON(v)
	}
	w.Header().Set("Vary", "Accept")
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && status == http.StatusOK {
		h := sha256.Sum256(b)
		etag := `"` + hex.EncodeToString(h[:8]) + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified) // klien sudah punya versi ini
			return
		}
	}
	w.WriteHeader(status)
	w.Write(b)
}

func idDari(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) gagalInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "rest: kesalahan internal", "err", err)
	tulisMasalah(w, r, http.StatusInternalServerError, "Kesalahan internal", "")
}

func (s *Server) daftar(w http.ResponseWriter, r *http.Request) {
	d, err := s.Toko.Daftar(r.Context())
	if err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	tulis(w, r, http.StatusOK, d)
}

func (s *Server) ambil(w http.ResponseWriter, r *http.Request) {
	id, ok := idDari(r)
	if !ok {
		tulisMasalah(w, r, http.StatusBadRequest, "ID tidak valid", "id harus bilangan bulat positif")
		return
	}
	p, err := s.Toko.Ambil(r.Context(), id)
	if errors.Is(err, toko.ErrTidakAda) {
		tulisMasalah(w, r, http.StatusNotFound, "Produk tidak ditemukan", "")
		return
	}
	if err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	tulis(w, r, http.StatusOK, p)
}

func (s *Server) ubahHarga(w http.ResponseWriter, r *http.Request) {
	id, ok := idDari(r)
	var isi struct {
		Harga int64 `json:"harga"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&isi) != nil {
		tulisMasalah(w, r, http.StatusBadRequest, "Request tidak valid", `kirim {"harga": angka}`)
		return
	}
	p, err := s.Toko.UbahHarga(toko.DenganOleh(r.Context(), "rest"), id, isi.Harga)
	switch {
	case errors.Is(err, toko.ErrTidakAda):
		tulisMasalah(w, r, http.StatusNotFound, "Produk tidak ditemukan", "")
	case errors.Is(err, toko.ErrValidasi):
		tulisMasalah(w, r, http.StatusUnprocessableEntity, "Harga tidak valid", "")
	case err != nil:
		s.gagalInternal(w, r, err)
	default:
		tulis(w, r, http.StatusOK, p)
	}
}

// bayar memotong saldo. Header Idempotency-Key membuat POST aman diulang.
func (s *Server) bayar(w http.ResponseWriter, r *http.Request) {
	var isi struct {
		Akun   string `json:"akun"`
		Jumlah int64  `json:"jumlah"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&isi) != nil {
		tulisMasalah(w, r, http.StatusBadRequest, "Request tidak valid", `kirim {"akun": "budi", "jumlah": 25000}`)
		return
	}
	if s.Gangguan.HarusGagal(r.Context()) {
		tulisMasalah(w, r, http.StatusServiceUnavailable, "Service sedang gangguan (fault injection)", "belum diproses, aman diulang")
		return
	}
	kunci := r.Header.Get("Idempotency-Key")
	// Pemrosesan tidak ikut batal bila klien pergi: seperti server sungguhan,
	// pekerjaan yang sudah dimulai tetap selesai walau klien sudah timeout.
	ctx, batal := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer batal()
	h, err := s.Toko.Bayar(ctx, kunci, isi.Akun, isi.Jumlah)
	lambat := s.Gangguan.AmbilLambat(ctx)
	slog.InfoContext(r.Context(), "bayar", "protokol", "rest", "akun", isi.Akun, "jumlah", isi.Jumlah,
		"idempotency_key", kunci, "dari_cache", h.DariCache, "err", errStr(err), "request_id", r.Header.Get("X-Request-Id"))
	// Jeda buatan SESUDAH commit: response "hilang" bila klien timeout lebih dulu.
	if lambat > 0 {
		select {
		case <-time.After(lambat):
		case <-r.Context().Done():
			return
		}
	}
	switch {
	case errors.Is(err, toko.ErrKunciBeda):
		tulisMasalah(w, r, http.StatusUnprocessableEntity, "Idempotency-Key dipakai untuk isi berbeda", "")
	case errors.Is(err, toko.ErrMasihProses):
		tulisMasalah(w, r, http.StatusConflict, "Masih diproses, coba lagi nanti", "")
	case errors.Is(err, toko.ErrSaldo):
		tulisMasalah(w, r, http.StatusUnprocessableEntity, "Saldo tidak cukup", "")
	case errors.Is(err, toko.ErrValidasi):
		tulisMasalah(w, r, http.StatusBadRequest, "Akun atau jumlah tidak valid", "")
	case err != nil:
		s.gagalInternal(w, r, err)
	default:
		if h.DariCache {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		tulis(w, r, http.StatusCreated, h)
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) saldo(w http.ResponseWriter, r *http.Request) {
	nilai, kali, err := s.Toko.Saldo(r.Context(), r.PathValue("akun"))
	if errors.Is(err, toko.ErrTidakAda) {
		tulisMasalah(w, r, http.StatusNotFound, "Akun tidak ditemukan", "")
		return
	}
	if err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(KodeJSON(map[string]any{"akun": r.PathValue("akun"), "saldo": nilai, "jumlah_pembayaran": kali}))
}

func (s *Server) resetSaldo(w http.ResponseWriter, r *http.Request) {
	if err := s.Toko.ResetSaldo(r.Context(), r.PathValue("akun"), 1000000); err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	s.saldo(w, r)
}

func (s *Server) ambilGangguan(w http.ResponseWriter, r *http.Request) {
	a, err := s.Gangguan.Get(r.Context())
	if err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(KodeJSON(a))
}

func (s *Server) setGangguan(w http.ResponseWriter, r *http.Request) {
	var a gangguan.Atur
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&a) != nil {
		tulisMasalah(w, r, http.StatusBadRequest, "Request tidak valid", "")
		return
	}
	a, err := s.Gangguan.Set(r.Context(), a)
	if err != nil {
		s.gagalInternal(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "gangguan diubah", "lambat_ms", a.LambatMs, "lambat_kali", a.LambatKali, "gagal_persen", a.GagalPersen)
	s.ambilGangguan(w, r)
}
