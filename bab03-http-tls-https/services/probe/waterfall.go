package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Timing adalah uraian waktu satu request, dalam ms relatif terhadap awal pemuatan halaman.
// Nilai -1 berarti tahap itu tidak terjadi (mis. koneksi dipakai ulang, tanpa DNS/TLS).
type Timing struct {
	URL       string  `json:"url"`
	Proto     string  `json:"proto"`
	Conn      int     `json:"conn"`
	Reused    bool    `json:"reused"`
	Status    int     `json:"status"`
	Bytes     int64   `json:"bytes"`
	Start     float64 `json:"start"`
	DNSStart  float64 `json:"dns_start"`
	DNSDone   float64 `json:"dns_done"`
	ConnStart float64 `json:"connect_start"`
	ConnDone  float64 `json:"connect_done"`
	TLSStart  float64 `json:"tls_start"`
	TLSDone   float64 `json:"tls_done"`
	GotConn   float64 `json:"got_conn"`
	TTFB      float64 `json:"ttfb"`
	End       float64 `json:"end"`
	Err       string  `json:"err,omitempty"`
}

// ConnTiming adalah waktu pembukaan satu koneksi (DNS, TCP/QUIC connect, TLS handshake).
type ConnTiming struct {
	ID        int     `json:"id"`
	DNSStart  float64 `json:"dns_start"`
	DNSDone   float64 `json:"dns_done"`
	ConnStart float64 `json:"connect_start"`
	ConnDone  float64 `json:"connect_done"`
	TLSStart  float64 `json:"tls_start"`
	TLSDone   float64 `json:"tls_done"`
	Version   string  `json:"tls_version"`
	ALPN      string  `json:"alpn"`
}

// Load adalah hasil satu kali "memuat halaman": HTML lalu semua asetnya.
type Load struct {
	Proto      string       `json:"proto"`
	Assets     int          `json:"assets"`
	Conns      int          `json:"conns"`
	TotalMs    float64      `json:"total_ms"`
	TLSVersion string       `json:"tls_version"`
	ALPN       string       `json:"alpn"`
	AltSvc     string       `json:"alt_svc,omitempty"`
	Requests   []Timing     `json:"requests"`
	ConnList   []ConnTiming `json:"connections"`
}

var linkRe = regexp.MustCompile(`href="([^"]+)"`)

// connLog mencatat setiap koneksi yang dibuka selama satu pemuatan halaman, dikunci dengan
// alamat lokal koneksi. Koneksi dicatat per koneksi (bukan per request) karena pada HTTP/1.1
// koneksi yang di-dial untuk request A bisa saja dipakai oleh request B yang lebih dulu siap.
type connLog struct {
	t0    time.Time
	mu    sync.Mutex
	conns map[string]*ConnTiming
	order []string
}

func (l *connLog) ms() float64 { return float64(time.Since(l.t0).Microseconds()) / 1000 }

func (l *connLog) add(local string, ct ConnTiming) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.conns[local]; ok {
		return
	}
	ct.ID = len(l.order) + 1
	l.conns[local] = &ct
	l.order = append(l.order, local)
}

// get mengembalikan catatan koneksi; koneksi yang tidak tercatat (seharusnya tidak terjadi) diberi nomor baru.
func (l *connLog) get(local string) ConnTiming {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.conns[local]; ok {
		return *c
	}
	c := &ConnTiming{ID: len(l.order) + 1, DNSStart: -1, DNSDone: -1, ConnStart: -1, ConnDone: -1, TLSStart: -1, TLSDone: -1}
	l.conns[local] = c
	l.order = append(l.order, local)
	return *c
}

func (l *connLog) list() []ConnTiming {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ConnTiming, 0, len(l.order))
	for _, k := range l.order {
		out = append(out, *l.conns[k])
	}
	return out
}

// dialTCPTLS membuka TCP lalu TLS dengan ALPN tertentu dan mencatat DNS/connect lewat httptrace.
// Transport bawaan Go tidak mengirim ALPN untuk koneksi khusus HTTP/1, padahal browser mengirim
// "http/1.1"; karena itu handshake TLS dibuat sendiri.
func (l *connLog) dialTCPTLS(roots *x509.CertPool, alpn string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ct := ConnTiming{DNSStart: -1, DNSDone: -1, ConnStart: -1, ConnDone: -1}
		var mu sync.Mutex
		set := func(p *float64) {
			mu.Lock()
			if *p < 0 {
				*p = l.ms()
			}
			mu.Unlock()
		}
		trace := &httptrace.ClientTrace{
			DNSStart:     func(httptrace.DNSStartInfo) { set(&ct.DNSStart) },
			DNSDone:      func(httptrace.DNSDoneInfo) { set(&ct.DNSDone) },
			ConnectStart: func(string, string) { set(&ct.ConnStart) },
			ConnectDone:  func(string, string, error) { set(&ct.ConnDone) },
		}
		raw, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(httptrace.WithClientTrace(ctx, trace), network, addr)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
		c := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: roots, NextProtos: []string{alpn}})
		mu.Lock()
		ct.TLSStart = l.ms()
		mu.Unlock()
		if err := c.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		st := c.ConnectionState()
		mu.Lock()
		ct.TLSDone = l.ms()
		ct.Version, ct.ALPN = tls.VersionName(st.Version), st.NegotiatedProtocol
		snap := ct
		mu.Unlock()
		l.add(c.LocalAddr().String(), snap)
		return c, nil
	}
}

// dialQUIC membuka koneksi QUIC; handshake transport dan TLS 1.3 terjadi bersamaan (1 RTT).
func (l *connLog) dialQUIC(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
	ct := ConnTiming{DNSStart: l.ms()}
	udp, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	ct.DNSDone = l.ms()
	pc, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	ct.ConnStart = l.ms()
	ct.TLSStart = ct.ConnStart
	tr := &quic.Transport{Conn: pc}
	conn, err := tr.Dial(ctx, udp, tlsCfg, cfg)
	if err != nil {
		_ = tr.Close()
		pc.Close()
		return nil, err
	}
	ct.ConnDone = l.ms()
	ct.TLSDone = ct.ConnDone
	st := conn.ConnectionState().TLS
	ct.Version, ct.ALPN = tls.VersionName(st.Version), st.NegotiatedProtocol
	l.add(conn.LocalAddr().String(), ct)
	go func() { // tutup socket UDP sesudah koneksi QUIC selesai
		<-conn.Context().Done()
		_ = tr.Close()
		pc.Close()
	}()
	return conn, nil
}

// newClient membuat klien baru (tanpa koneksi hangat) untuk protokol tertentu.
func newClient(proto string, roots *x509.CertPool, l *connLog) (*http.Client, func()) {
	switch proto {
	case "h3":
		tr := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, Dial: l.dialQUIC}
		return &http.Client{Transport: tr, Timeout: 30 * time.Second}, func() { _ = tr.Close() }
	case "h2":
		tr := &http.Transport{DialTLSContext: l.dialTCPTLS(roots, "h2"), ForceAttemptHTTP2: true}
		return &http.Client{Transport: tr, Timeout: 30 * time.Second}, tr.CloseIdleConnections
	default: // h1: seperti browser, maksimal 6 koneksi per host
		tr := &http.Transport{
			DialTLSContext:      l.dialTCPTLS(roots, "http/1.1"),
			MaxConnsPerHost:     6,
			MaxIdleConnsPerHost: 6,
		}
		return &http.Client{Transport: tr, Timeout: 30 * time.Second}, tr.CloseIdleConnections
	}
}

type fetchResult struct {
	t      Timing
	body   []byte
	altSvc string
}

// fetch menjalankan satu GET; httptrace mencatat kapan koneksi didapat dan kapan byte pertama tiba.
func fetch(ctx context.Context, cl *http.Client, url string, l *connLog, keepBody bool) fetchResult {
	t := Timing{URL: url, DNSStart: -1, DNSDone: -1, ConnStart: -1, ConnDone: -1, TLSStart: -1, TLSDone: -1, GotConn: -1, TTFB: -1}
	var mu sync.Mutex
	var local string
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			defer mu.Unlock()
			if t.GotConn < 0 {
				t.GotConn = l.ms()
				t.Reused = info.Reused
				if info.Conn != nil {
					local = info.Conn.LocalAddr().String()
				}
			}
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			if t.TTFB < 0 {
				t.TTFB = l.ms()
			}
			mu.Unlock()
		},
	}
	finish := func(err error) fetchResult {
		mu.Lock()
		defer mu.Unlock()
		t.End = l.ms()
		if err != nil {
			t.Err = err.Error()
		}
		if t.TTFB < 0 && err == nil {
			t.TTFB = t.End
		}
		if local != "" {
			t.Conn = l.get(local).ID
		}
		return fetchResult{t: t}
	}
	mu.Lock()
	t.Start = l.ms()
	mu.Unlock()
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", url, nil)
	if err != nil {
		return finish(err)
	}
	res, err := cl.Do(req)
	if err != nil {
		return finish(err)
	}
	defer res.Body.Close()
	var body []byte
	var n int64
	if keepBody {
		body, _ = io.ReadAll(res.Body)
		n = int64(len(body))
	} else {
		n, _ = io.Copy(io.Discard, res.Body)
	}
	out := finish(nil)
	out.t.Bytes, out.t.Status, out.t.Proto = n, res.StatusCode, res.Proto
	out.body, out.altSvc = body, res.Header.Get("Alt-Svc")
	return out
}

// LoadPage meniru browser: ambil HTML, cari semua aset, lalu ambil aset secara paralel.
func LoadPage(ctx context.Context, proto, base string, roots *x509.CertPool, n, think int) Load {
	l := &connLog{t0: time.Now(), conns: map[string]*ConnTiming{}}
	cl, closeFn := newClient(proto, roots, l)
	defer closeFn()
	page := fmt.Sprintf("%s/page?n=%d&think=%d", base, n, think)
	first := fetch(ctx, cl, page, l, true)
	ld := Load{Proto: proto, Requests: []Timing{first.t}, AltSvc: first.altSvc}
	if first.t.Err == "" {
		var links []string
		for _, m := range linkRe.FindAllStringSubmatch(string(first.body), -1) {
			links = append(links, base+m[1])
		}
		ld.Assets = len(links)
		res := make([]Timing, len(links))
		var wg sync.WaitGroup
		for i, u := range links {
			wg.Add(1)
			go func(i int, u string) {
				defer wg.Done()
				res[i] = fetch(ctx, cl, u, l, false).t
			}(i, u)
		}
		wg.Wait()
		sort.SliceStable(res, func(i, j int) bool { return res[i].Start < res[j].Start })
		ld.Requests = append(ld.Requests, res...)
	}
	for _, r := range ld.Requests {
		ld.TotalMs = math.Max(ld.TotalMs, r.End)
	}
	ld.ConnList = l.list()
	ld.Conns = len(ld.ConnList)
	attachHandshakes(ld.Requests, ld.ConnList)
	if len(ld.ConnList) > 0 {
		ld.TLSVersion, ld.ALPN = ld.ConnList[0].Version, ld.ConnList[0].ALPN
	}
	return ld
}

// attachHandshakes menempelkan waktu DNS/connect/TLS ke request pertama yang memakai tiap koneksi.
// Flag Reused dari httptrace tidak dipakai untuk ini: pada HTTP/1.1, koneksi baru yang diserahkan
// ke request lain lewat antrean idle dilaporkan sebagai "reused" walaupun belum pernah dipakai.
func attachHandshakes(reqs []Timing, conns []ConnTiming) {
	byID := map[int]ConnTiming{}
	for _, c := range conns {
		byID[c.ID] = c
	}
	idx := make([]int, len(reqs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return reqs[idx[a]].GotConn < reqs[idx[b]].GotConn })
	seen := map[int]bool{}
	for _, i := range idx {
		t := &reqs[i]
		c, ok := byID[t.Conn]
		if t.Conn == 0 || t.GotConn < 0 || !ok {
			continue
		}
		t.Reused = seen[t.Conn]
		if !t.Reused {
			seen[t.Conn] = true
			t.DNSStart, t.DNSDone, t.ConnStart, t.ConnDone, t.TLSStart, t.TLSDone =
				c.DNSStart, c.DNSDone, c.ConnStart, c.ConnDone, c.TLSStart, c.TLSDone
		}
	}
}

// Median mengembalikan median dari xs (xs tidak diubah).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// ModelRTT menghitung jumlah RTT ideal menurut contoh di buku (subbab "Menghitung cost HTTP/1.1"):
// h1 = 3 (TCP+TLS+HTML) + 2 (buka 5 koneksi lagi) + ceil(n/6); h2 = 3 + 1; h3 = 2 + 1.
func ModelRTT(proto string, n int) int {
	switch proto {
	case "h3":
		return 3
	case "h2":
		return 4
	default:
		extra := 2
		if n <= 1 {
			extra = 0
		}
		return 3 + extra + int(math.Ceil(float64(n)/6))
	}
}
