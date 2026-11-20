package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Front adalah klien ke nginx (reverse proxy) dan ke origin (jalur admin internal).
type Front struct {
	NginxHTTPS string   // mis. https://nginx
	NginxHTTP  string   // mis. http://nginx
	Origins    []string // mis. http://origin-a:8080
	client     *http.Client
	plain      *http.Client
}

// NewFront membuat klien h2 ke nginx (koneksi dipakai ulang) tanpa dekompresi otomatis.
func NewFront(httpsURL, httpURL string, origins []string, roots *x509.CertPool) *Front {
	tr := &http.Transport{
		TLSClientConfig:    &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2:  true,
		DisableCompression: true, // agar ukuran gzip di kabel terlihat apa adanya
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Front{
		NginxHTTPS: httpsURL, NginxHTTP: httpURL, Origins: origins,
		client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: noRedirect},
		plain:  &http.Client{Timeout: 5 * time.Second, CheckRedirect: noRedirect},
	}
}

// CacheResult adalah hasil satu request lewat nginx untuk panel cache.
type CacheResult struct {
	Path        string            `json:"path"`
	Status      int               `json:"status"`
	Proto       string            `json:"proto"`
	WireBytes   int               `json:"wire_bytes"`
	DurMs       float64           `json:"dur_ms"`
	Headers     map[string]string `json:"headers"`
	OriginHits  int               `json:"origin_hits"`
	OriginDelta int               `json:"origin_delta"`
	SentINM     string            `json:"sent_if_none_match,omitempty"`
	SentAE      string            `json:"sent_accept_encoding,omitempty"`
	Err         string            `json:"err,omitempty"`
}

var shownHeaders = []string{"Cache-Control", "ETag", "X-Cache-Status", "Age", "Content-Encoding", "Content-Length",
	"Vary", "Strict-Transport-Security", "X-Upstream", "X-Instance", "Location", "Server"}

func pickHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for _, k := range shownHeaders {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

// originHits menjumlah counter hit dari semua origin (untuk melihat apakah request sampai ke origin).
func (f *Front) originHits(ctx context.Context, keys ...string) int {
	total := 0
	for _, o := range f.Origins {
		st, err := f.originStats(ctx, o)
		if err != nil {
			continue
		}
		for _, k := range keys {
			total += st.Hits[k]
		}
	}
	return total
}

// OriginStats adalah isi /admin/stats sebuah origin.
type OriginStats struct {
	Instance string         `json:"instance"`
	Versi    int            `json:"versi"`
	Sakit    bool           `json:"sakit"`
	Hits     map[string]int `json:"hits"`
	Err      string         `json:"err,omitempty"`
}

func (f *Front) originStats(ctx context.Context, origin string) (OriginStats, error) {
	var st OriginStats
	req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/admin/stats", nil)
	res, err := f.plain.Do(req)
	if err != nil {
		return st, err
	}
	defer res.Body.Close()
	err = json.NewDecoder(res.Body).Decode(&st)
	return st, err
}

// Get meminta path lewat nginx HTTPS dan mencatat header cache serta apakah origin ikut dipanggil.
func (f *Front) Get(ctx context.Context, path, inm string, gzip bool) CacheResult {
	keys := []string{"katalog_200", "katalog_304", "static"}
	before := f.originHits(ctx, keys...)
	r := CacheResult{Path: path, SentINM: inm}
	req, _ := http.NewRequestWithContext(ctx, "GET", f.NginxHTTPS+path, nil)
	if inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	if gzip {
		r.SentAE = "gzip"
		req.Header.Set("Accept-Encoding", "gzip")
	}
	t0 := time.Now()
	res, err := f.client.Do(req)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	n, _ := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	r.DurMs = float64(time.Since(t0).Microseconds()) / 1000
	r.Status, r.Proto, r.WireBytes, r.Headers = res.StatusCode, res.Proto, int(n), pickHeaders(res.Header)
	after := f.originHits(ctx, keys...)
	r.OriginHits, r.OriginDelta = after, after-before
	return r
}

// post memanggil endpoint admin di semua origin.
func (f *Front) post(ctx context.Context, origins []string, path string) []json.RawMessage {
	var out []json.RawMessage
	for _, o := range origins {
		req, _ := http.NewRequestWithContext(ctx, "POST", o+path, nil)
		res, err := f.plain.Do(req)
		if err != nil {
			b, _ := json.Marshal(map[string]string{"origin": o, "err": err.Error()})
			out = append(out, b)
			continue
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		out = append(out, json.RawMessage(strings.TrimSpace(string(b))))
	}
	return out
}

// Bump menaikkan versi katalog di semua origin, sehingga ETag berubah.
func (f *Front) Bump(ctx context.Context) []json.RawMessage {
	return f.post(ctx, f.Origins, "/admin/bump")
}

// HSTSResult memperlihatkan redirect HTTP ke HTTPS dan header HSTS.
type HSTSResult struct {
	HTTP  CacheResult `json:"http"`
	HTTPS CacheResult `json:"https"`
}

// HSTS meminta halaman yang sama lewat http:// dan https:// nginx.
func (f *Front) HSTS(ctx context.Context) HSTSResult {
	var out HSTSResult
	do := func(cl *http.Client, url string) CacheResult {
		r := CacheResult{Path: url}
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		t0 := time.Now()
		res, err := cl.Do(req)
		if err != nil {
			r.Err = err.Error()
			return r
		}
		n, _ := io.Copy(io.Discard, res.Body)
		res.Body.Close()
		r.DurMs = float64(time.Since(t0).Microseconds()) / 1000
		r.Status, r.Proto, r.WireBytes, r.Headers = res.StatusCode, res.Proto, int(n), pickHeaders(res.Header)
		return r
	}
	out.HTTP = do(f.plain, f.NginxHTTP+"/api/katalog")
	out.HTTPS = do(f.client, f.NginxHTTPS+"/api/katalog")
	return out
}

// LBHit adalah satu request load balancing.
type LBHit struct {
	N        int     `json:"n"`
	Status   int     `json:"status"`
	Instance string  `json:"instance"`
	Upstream string  `json:"upstream"`
	DurMs    float64 `json:"dur_ms"`
	Err      string  `json:"err,omitempty"`
}

// LBResult merangkum distribusi request ke instance origin.
type LBResult struct {
	Hits   []LBHit        `json:"hits"`
	Counts map[string]int `json:"counts"`
	Stats  []OriginStats  `json:"stats"`
}

// upstreamNames memetakan alamat IP upstream nginx ke nama service (origin-a, origin-b).
func (f *Front) upstreamNames() map[string]string {
	m := map[string]string{}
	for _, o := range f.Origins {
		host := strings.TrimPrefix(o, "http://")
		h, port, _ := net.SplitHostPort(host)
		ips, _ := net.LookupHost(h)
		for _, ip := range ips {
			m[net.JoinHostPort(ip, port)] = h
		}
	}
	return m
}

// LB mengirim n request ke /api/siapa lewat nginx dan mencatat instance yang menjawab.
func (f *Front) LB(ctx context.Context, n int) LBResult {
	names := f.upstreamNames()
	out := LBResult{Counts: map[string]int{}}
	for i := 1; i <= n; i++ {
		h := LBHit{N: i}
		req, _ := http.NewRequestWithContext(ctx, "GET", f.NginxHTTPS+"/api/siapa", nil)
		t0 := time.Now()
		res, err := f.client.Do(req)
		if err != nil {
			h.Err = err.Error()
			out.Counts["error"]++
			out.Hits = append(out.Hits, h)
			continue
		}
		var body struct {
			Instance string `json:"instance"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		h.DurMs = float64(time.Since(t0).Microseconds()) / 1000
		h.Status = res.StatusCode
		// $upstream_addr berisi semua upstream yang dicoba, mis. "172.20.0.5:8080, 172.20.0.4:8080".
		var tried []string
		for _, a := range strings.Split(res.Header.Get("X-Upstream"), ",") {
			a = strings.TrimSpace(a)
			if nm, ok := names[a]; ok {
				a = nm
			}
			if a != "" {
				tried = append(tried, a)
			}
		}
		h.Upstream = strings.Join(tried, " → ")
		h.Instance = body.Instance
		if res.StatusCode != 200 || h.Instance == "" {
			h.Instance = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		out.Counts[h.Instance]++
		out.Hits = append(out.Hits, h)
	}
	out.Stats = f.Stats(ctx)
	return out
}

// Stats mengambil /admin/stats semua origin.
func (f *Front) Stats(ctx context.Context) []OriginStats {
	var out []OriginStats
	for _, o := range f.Origins {
		st, err := f.originStats(ctx, o)
		if err != nil {
			st.Instance = strings.TrimPrefix(o, "http://")
			st.Err = err.Error()
		}
		out = append(out, st)
	}
	return out
}

// SetSick menyalakan atau mematikan mode "sakit" (503) pada satu origin.
func (f *Front) SetSick(ctx context.Context, instance string, on bool) ([]json.RawMessage, error) {
	for _, o := range f.Origins {
		if strings.Contains(o, "//"+instance+":") {
			v := "0"
			if on {
				v = "1"
			}
			return f.post(ctx, []string{o}, "/admin/sakit?on="+v), nil
		}
	}
	return nil, fmt.Errorf("instance %q tidak dikenal", instance)
}
