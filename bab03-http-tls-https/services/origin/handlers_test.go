package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestApp() *App { return NewApp("uji", slog.New(slog.NewTextHandler(io.Discard, nil))) }

func TestEtagMatch(t *testing.T) {
	cases := []struct {
		inm, etag string
		want      bool
	}{
		{`"v1"`, `"v1"`, true},
		{`W/"v1"`, `"v1"`, true}, // nginx gzip mengubah ETag menjadi weak
		{`"v0", "v1"`, `"v1"`, true},
		{`"v2"`, `"v1"`, false},
		{`*`, `"v1"`, true},
	}
	for _, c := range cases {
		if got := etagMatch(c.inm, c.etag); got != c.want {
			t.Errorf("etagMatch(%q,%q)=%v mau %v", c.inm, c.etag, got, c.want)
		}
	}
}

func TestKatalog304DanBump(t *testing.T) {
	a := newTestApp()
	h := a.Routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/katalog", nil))
	if rec.Code != 200 || rec.Header().Get("ETag") != `"v1"` {
		t.Fatalf("mau 200 ETag v1, dapat %d %q", rec.Code, rec.Header().Get("ETag"))
	}
	req := httptest.NewRequest("GET", "/api/katalog", nil)
	req.Header.Set("If-None-Match", `"v1"`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("mau 304 tanpa body, dapat %d (%d byte)", rec.Code, rec.Body.Len())
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/admin/bump", nil))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("ETag") != `"v2"` {
		t.Fatalf("sesudah bump mau 200 v2, dapat %d %q", rec.Code, rec.Header().Get("ETag"))
	}
	if a.snapshot()["katalog_304"] != 1 || a.snapshot()["katalog_200"] != 2 {
		t.Fatalf("counter salah: %v", a.snapshot())
	}
}

func TestPageDanSakit(t *testing.T) {
	a := newTestApp()
	h := a.Routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/page?n=7", nil))
	if c := strings.Count(rec.Body.String(), "<link "); c != 7 {
		t.Fatalf("mau 7 aset, dapat %d", c)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/asset/1.css?size=100", nil))
	if rec.Body.Len() != 100 {
		t.Fatalf("ukuran aset %d", rec.Body.Len())
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/admin/sakit?on=1", nil))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/siapa", nil))
	if rec.Code != 503 {
		t.Fatalf("instance sakit mau 503, dapat %d", rec.Code)
	}
}
