// Service monitor: heartbeat ke setiap node, generator lalu lintas lewat nginx,
// pengukuran (RTT, throughput, monolith vs jaringan), dan siaran status ke UI lewat SSE.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nodeInfo struct {
	Nama  string
	URL   string // port utama
	Admin string // port admin
}

// Keadaan satu node menurut monitor.
type NodeState struct {
	Nama          string          `json:"nama"`
	Status        string          `json:"status"`
	RTTMs         float64         `json:"rtt_ms"`
	GagalBeruntun int             `json:"gagal_beruntun"`
	TerakhirOKMs  int64           `json:"terakhir_ok_ms"`
	OffsetMs      float64         `json:"offset_ms"` // perkiraan selisih jam node terhadap jam monitor
	AHeartbeat    float64         `json:"a_heartbeat"`
	Fault         json.RawMessage `json:"fault"` // null bila port admin tidak menjawab (misalnya sedang restart)
	jendela       *Jendela
}

type LaluLintas struct {
	Aktif  bool   `json:"aktif"`
	RPS    int    `json:"rps"`
	Metode string `json:"metode"` // baca (GET, idempotent) atau tulis (POST)
}

type DBStat struct {
	OK              bool             `json:"ok"`
	Total           int64            `json:"total"`
	PerNode         map[string]int64 `json:"per_node"`
	UrutanJamMundur int64            `json:"urutan_jam_mundur"`
}

type monitor struct {
	log    *slog.Logger
	nodes  []nodeInfo
	lb     string
	seri   *Seri
	mu     sync.Mutex
	state  map[string]*NodeState
	lbOK   *Jendela
	lalu   LaluLintas
	db     DBStat
	ukurMu sync.Mutex
	hc     *http.Client // heartbeat
	tc     *http.Client // lalu lintas
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "monitor")
	m := &monitor{
		log:   log,
		nodes: parseNodes(env("NODES", "node1=http://node1:8080|http://node1:9090")),
		lb:    env("LB_URL", "http://nginx:8080"),
		seri:  NewSeri(60),
		state: map[string]*NodeState{},
		lbOK:  &Jendela{n: 120},
		lalu:  LaluLintas{Aktif: true, RPS: 8, Metode: "baca"},
		hc:    &http.Client{Timeout: time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 4}},
		tc:    &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64}},
	}
	for _, n := range m.nodes {
		m.state[n.Nama] = &NodeState{Nama: n.Nama, Status: StatusDicurigai, Fault: json.RawMessage("null"), jendela: &Jendela{n: 120}}
		go m.heartbeat(n)
		go m.pantauFault(n)
	}
	go m.heartbeatLB()
	go m.generator()
	go m.pantauDB()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sehat", func(w http.ResponseWriter, r *http.Request) { tulisJSON(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		tulisJSON(w, 200, map[string]int64{"t": time.Now().UnixMilli()})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { tulisJSON(w, 200, m.snapshot()) })
	mux.HandleFunc("GET /api/stream", m.stream)
	mux.HandleFunc("POST /api/lalu-lintas", m.aturLaluLintas)
	mux.HandleFunc("POST /api/reset", m.reset)
	mux.HandleFunc("POST /api/fault/{node}", m.proxyAdmin("/admin/fault"))
	mux.HandleFunc("POST /api/crash/{node}", m.proxyAdmin("/admin/crash"))
	mux.HandleFunc("POST /api/ukur/rtt", m.ukurRTT)
	mux.HandleFunc("POST /api/ukur/throughput", m.ukurThroughput)
	mux.HandleFunc("POST /api/ukur/bandingkan", m.ukurBandingkan)

	port := env("PORT", "8080")
	log.Info("monitor siap", "port", port, "nodes", len(m.nodes), "lb", m.lb)
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server berhenti", "err", err.Error())
		os.Exit(1)
	}
}

// parseNodes membaca "node1=http://a:8080|http://a:9090,node2=..."
func parseNodes(s string) []nodeInfo {
	var out []nodeInfo
	for _, bag := range strings.Split(s, ",") {
		nama, sisa, ok := strings.Cut(strings.TrimSpace(bag), "=")
		if !ok {
			continue
		}
		utama, admin, _ := strings.Cut(sisa, "|")
		out = append(out, nodeInfo{Nama: nama, URL: utama, Admin: admin})
	}
	return out
}

// heartbeat memanggil /sehat setiap 500 ms. Sekaligus memperkirakan offset jam node
// dengan cara Cristian: offset = jam_node - (t_kirim + RTT/2).
func (m *monitor) heartbeat(n nodeInfo) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		t0 := time.Now()
		var isi struct {
			WaktuNodeMs int64 `json:"waktu_node_ms"`
		}
		ok := false
		resp, err := m.hc.Get(n.URL + "/sehat")
		rtt := time.Since(t0)
		if err == nil {
			ok = resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&isi) == nil
			resp.Body.Close()
		}
		m.mu.Lock()
		st := m.state[n.Nama]
		sebelum := st.Status
		st.jendela.Tambah(ok)
		st.AHeartbeat = st.jendela.Rasio()
		if ok {
			st.GagalBeruntun = 0
			st.RTTMs = float64(rtt.Microseconds()) / 1000
			st.TerakhirOKMs = time.Now().UnixMilli()
			tengah := t0.Add(rtt / 2).UnixMilli()
			st.OffsetMs = float64(isi.WaktuNodeMs - tengah)
		} else {
			st.GagalBeruntun++
			st.RTTMs = 0
		}
		st.Status = Klasifikasi(ok, rtt, st.GagalBeruntun)
		sesudah := st.Status
		m.mu.Unlock()
		if sebelum != sesudah {
			m.log.Warn("status node berubah", "node", n.Nama, "dari", sebelum, "ke", sesudah, "rtt_ms", rtt.Milliseconds())
		}
	}
}

func (m *monitor) heartbeatLB() {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		resp, err := m.hc.Get(m.lb + "/nginx-sehat")
		ok := err == nil && resp.StatusCode == 200
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		m.mu.Lock()
		m.lbOK.Tambah(ok)
		m.mu.Unlock()
	}
}

func (m *monitor) pantauFault(n nodeInfo) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for range tick.C {
		raw := json.RawMessage("null")
		resp, err := m.hc.Get(n.Admin + "/admin/fault")
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 200 && json.Valid(b) {
				raw = bytes.TrimSpace(b)
			}
		}
		m.mu.Lock()
		m.state[n.Nama].Fault = raw
		m.mu.Unlock()
	}
}

func (m *monitor) pantauDB() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for range tick.C {
		var st DBStat
		resp, err := m.hc.Get(m.lb + "/api/statistik")
		if err == nil {
			var isi struct {
				PerNode         map[string]int64 `json:"per_node"`
				Total           int64            `json:"total"`
				UrutanJamMundur int64            `json:"urutan_jam_mundur"`
			}
			if resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&isi) == nil {
				st = DBStat{OK: true, Total: isi.Total, PerNode: isi.PerNode, UrutanJamMundur: isi.UrutanJamMundur}
			}
			resp.Body.Close()
		}
		m.mu.Lock()
		if st.OK || !m.db.OK {
			m.db = st
		} // bila gagal sesaat (misalnya kena drop), pertahankan angka terakhir
		m.mu.Unlock()
	}
}

// generator mengirim request ke nginx dengan laju tetap (open loop), seperti pengguna sungguhan
// yang tidak menunggu pengguna lain selesai.
func (m *monitor) generator() {
	for {
		m.mu.Lock()
		cfg := m.lalu
		m.mu.Unlock()
		if !cfg.Aktif || cfg.RPS <= 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go func() {
			h := m.kirim(context.Background(), cfg.Metode == "tulis")
			m.seri.Tambah(time.Now(), h)
		}()
		time.Sleep(time.Second / time.Duration(cfg.RPS))
	}
}

// kirim mengirim satu request lewat nginx dan menggolongkan hasilnya.
func (m *monitor) kirim(ctx context.Context, tulis bool) Hasil {
	method, path := http.MethodGet, "/api/sapa"
	if tulis {
		method, path = http.MethodPost, "/api/catat"
	}
	req, _ := http.NewRequestWithContext(ctx, method, m.lb+path, nil)
	t0 := time.Now()
	resp, err := m.tc.Do(req)
	lat := float64(time.Since(t0).Microseconds()) / 1000
	h := Hasil{Latency: lat, Tulis: tulis}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			h.Jenis = "timeout_klien"
		} else {
			h.Jenis = "koneksi"
		}
		return h
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	h.Retry = strings.Contains(resp.Header.Get("X-Upstream"), ",")
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		h.OK = true
		h.Node = resp.Header.Get("X-Node")
		return h
	}
	switch resp.StatusCode {
	case 502:
		h.Jenis = "502_bad_gateway"
	case 504:
		h.Jenis = "504_gateway_timeout"
	default:
		h.Jenis = "http_" + strconv.Itoa(resp.StatusCode)
	}
	return h
}

type Snapshot struct {
	WaktuMs      int64        `json:"waktu_ms"`
	Nodes        []NodeState  `json:"nodes"`
	LaluLintas   LaluLintas   `json:"lalu_lintas"`
	Seri         []Bucket     `json:"seri"`
	Kumulatif    Kumulatif    `json:"kumulatif"`
	DB           DBStat       `json:"db"`
	Availability Availability `json:"availability"`
}

type Availability struct {
	PerNode         map[string]float64 `json:"per_node"`
	LB              float64            `json:"lb"`
	PrediksiParalel float64            `json:"prediksi_paralel"` // 1 - prod(1-a_i)
	PrediksiJalur   float64            `json:"prediksi_jalur"`   // a_lb x paralel (seri)
	TerukurSistem   float64            `json:"terukur_sistem"`   // sukses/kirim 60 detik terakhir
}

func (m *monitor) snapshot() Snapshot {
	seri, kum := m.seri.Snapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{WaktuMs: time.Now().UnixMilli(), LaluLintas: m.lalu, Seri: seri, Kumulatif: kum, DB: m.db}
	av := Availability{PerNode: map[string]float64{}, LB: m.lbOK.Rasio()}
	var as []float64
	for _, n := range m.nodes {
		st := *m.state[n.Nama]
		st.jendela = nil
		s.Nodes = append(s.Nodes, st)
		av.PerNode[n.Nama] = st.AHeartbeat
		as = append(as, st.AHeartbeat)
	}
	av.PrediksiParalel = AvailabilityParalel(as)
	av.PrediksiJalur = av.LB * av.PrediksiParalel
	ok, total := 0, 0
	for _, b := range seri {
		ok += b.Sukses
		total += b.Sukses + b.Gagal
	}
	if total > 0 {
		av.TerukurSistem = float64(ok) / float64(total)
	}
	s.Availability = av
	return s
}

// stream mengirim snapshot setiap detik sebagai Server-Sent Events.
func (m *monitor) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming tidak didukung", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		b, _ := json.Marshal(m.snapshot())
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return
		}
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (m *monitor) aturLaluLintas(w http.ResponseWriter, r *http.Request) {
	var c LaluLintas
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&c); err != nil {
		tulisJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
		return
	}
	if c.RPS < 0 || c.RPS > 50 || (c.Metode != "baca" && c.Metode != "tulis") {
		tulisJSON(w, 400, map[string]string{"error": "rps 0..50, metode baca|tulis"})
		return
	}
	m.mu.Lock()
	m.lalu = c
	m.mu.Unlock()
	m.log.Info("lalu lintas diubah", "aktif", c.Aktif, "rps", c.RPS, "metode", c.Metode)
	tulisJSON(w, 200, c)
}

func (m *monitor) reset(w http.ResponseWriter, r *http.Request) {
	m.seri.Reset()
	m.mu.Lock()
	for _, st := range m.state {
		st.jendela = &Jendela{n: 120}
	}
	m.lbOK = &Jendela{n: 120}
	m.mu.Unlock()
	// Kosongkan tabel lewat nginx. Bila node sedang di-drop, coba beberapa kali.
	var err error
	for i := 0; i < 5; i++ {
		var resp *http.Response
		resp, err = m.hc.Post(m.lb+"/api/kosongkan", "application/json", nil)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	m.log.Warn("reset statistik", "db_ok", err == nil)
	tulisJSON(w, 200, map[string]any{"ok": true, "db_dikosongkan": err == nil})
}

// proxyAdmin meneruskan perintah dari UI ke port admin node (yang tidak lewat nginx).
func (m *monitor) proxyAdmin(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nama := r.PathValue("node")
		var n *nodeInfo
		for i := range m.nodes {
			if m.nodes[i].Nama == nama {
				n = &m.nodes[i]
			}
		}
		if n == nil {
			tulisJSON(w, 404, map[string]string{"error": "node tidak dikenal"})
			return
		}
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		resp, err := m.hc.Post(n.Admin+path, "application/json", bytes.NewReader(body))
		if err != nil {
			tulisJSON(w, 502, map[string]string{"error": "port admin node tidak menjawab (mungkin sedang restart)"})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		m.log.Warn("perintah admin", "node", nama, "path", path, "status", resp.StatusCode)
	}
}

// ---------- Mengukur Sendiri ----------

type Ringkas struct {
	Sampel int     `json:"sampel"`
	Gagal  int     `json:"gagal"`
	MinMs  float64 `json:"min_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	MaksMs float64 `json:"maks_ms"`
}

func ringkas(s []float64, gagal int) Ringkas {
	return Ringkas{Sampel: len(s), Gagal: gagal, MinMs: Persentil(s, 0), P50Ms: Persentil(s, 50), P95Ms: Persentil(s, 95), MaksMs: Persentil(s, 100)}
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// ukurRTT: TCP connect (kira-kira satu RTT jaringan) dan HTTP GET /ping (RTT + kerja server).
func (m *monitor) ukurRTT(w http.ResponseWriter, r *http.Request) {
	const n = 20
	type target struct {
		Nama string  `json:"nama"`
		TCP  Ringkas `json:"tcp_connect"`
		HTTP Ringkas `json:"http_ping"`
	}
	var hasil []target
	ukur := func(nama, base, pingPath string) {
		u, _ := url.Parse(base)
		var tcp, httpS []float64
		gt, gh := 0, 0
		for i := 0; i < n; i++ {
			t0 := time.Now()
			c, err := net.DialTimeout("tcp", u.Host, time.Second)
			if err != nil {
				gt++
				continue
			}
			tcp = append(tcp, ms(time.Since(t0)))
			c.Close()
		}
		cl := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
		for i := 0; i <= n; i++ {
			t0 := time.Now()
			resp, err := cl.Get(base + pingPath)
			if err != nil {
				gh++
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if i > 0 { // panggilan pertama membuka koneksi; tidak dihitung
				httpS = append(httpS, ms(time.Since(t0)))
			}
		}
		hasil = append(hasil, target{Nama: nama, TCP: ringkas(tcp, gt), HTTP: ringkas(httpS, gh)})
	}
	for _, nd := range m.nodes {
		ukur(nd.Nama, nd.URL, "/ping")
	}
	ukur("nginx (LB saja)", m.lb, "/nginx-sehat")
	ukur("nginx → node", m.lb, "/ping")
	tulisJSON(w, 200, map[string]any{"sampel_per_target": n, "hasil": hasil})
}

// ukurThroughput: closed loop, k worker mengirim request sebanyak mungkin selama d detik.
func (m *monitor) ukurThroughput(w http.ResponseWriter, r *http.Request) {
	var p struct {
		DurasiS    int    `json:"durasi_s"`
		Konkurensi int    `json:"konkurensi"`
		Metode     string `json:"metode"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&p)
	if p.DurasiS < 1 || p.DurasiS > 10 {
		p.DurasiS = 3
	}
	if p.Konkurensi < 1 || p.Konkurensi > 32 {
		p.Konkurensi = 8
	}
	if !m.ukurMu.TryLock() {
		tulisJSON(w, 409, map[string]string{"error": "pengukuran lain sedang berjalan"})
		return
	}
	defer m.ukurMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(p.DurasiS)*time.Second)
	defer cancel()
	var mu sync.Mutex
	var lat []float64
	gagal := 0
	perNode := map[string]int{}
	var wg sync.WaitGroup
	mulai := time.Now()
	for i := 0; i < p.Konkurensi; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				h := m.kirim(ctx, p.Metode == "tulis")
				if ctx.Err() != nil && !h.OK {
					return // request terpotong karena waktu habis, tidak dihitung
				}
				mu.Lock()
				if h.OK {
					lat = append(lat, h.Latency)
					perNode[h.Node]++
				} else {
					gagal++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	durasi := time.Since(mulai).Seconds()
	m.log.Info("uji throughput", "durasi_s", p.DurasiS, "konkurensi", p.Konkurensi, "sukses", len(lat), "gagal", gagal)
	tulisJSON(w, 200, map[string]any{
		"durasi_s": durasi, "konkurensi": p.Konkurensi, "metode": p.Metode,
		"sukses": len(lat), "gagal": gagal, "rps": float64(len(lat)) / durasi,
		"latency": ringkas(lat, gagal), "p99_ms": Persentil(lat, 99), "per_node": perNode,
	})
}

// Kerja identik dengan versi di service node.
func Kerja(x int) int {
	s := 0
	for i := 0; i < 64; i++ {
		s += (x + i) * (x ^ i) % 97
	}
	return s
}

// ukurBandingkan: n panggilan fungsi lokal (monolith) vs n panggilan jaringan ke node (terdistribusi).
func (m *monitor) ukurBandingkan(w http.ResponseWriter, r *http.Request) {
	var p struct {
		N int `json:"n"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&p)
	if p.N < 1 || p.N > 1000 {
		p.N = 100
	}
	// Monolith: panggilan fungsi biasa di proses yang sama.
	t0 := time.Now()
	jumlahLokal := 0
	for i := 0; i < p.N; i++ {
		jumlahLokal += Kerja(i)
	}
	lokal := time.Since(t0)

	// Terdistribusi: fungsi yang sama, tetapi setiap panggilan lewat HTTP ke node pertama
	// yang sehat, lalu lewat nginx (tambah satu hop).
	panggil := func(base string) (time.Duration, int, error) {
		cl := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
		// panaskan koneksi agar yang diukur adalah panggilan, bukan handshake TCP
		if resp, err := cl.Get(base + "/api/hitung?x=0"); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		jumlah := 0
		t := time.Now()
		for i := 0; i < p.N; i++ {
			resp, err := cl.Get(base + "/api/hitung?x=" + strconv.Itoa(i))
			if err != nil {
				return 0, 0, err
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			v, _ := strconv.Atoi(string(b))
			jumlah += v
		}
		return time.Since(t), jumlah, nil
	}
	target := m.nodePertamaSehat()
	langsung, jumlahLangsung, err1 := panggil(target.URL)
	viaLB, jumlahLB, err2 := panggil(m.lb)
	type baris struct {
		Nama      string  `json:"nama"`
		TotalMs   float64 `json:"total_ms"`
		PerCallUs float64 `json:"per_call_us"`
		Jumlah    int     `json:"jumlah"`
		Error     string  `json:"error,omitempty"`
	}
	mk := func(nama string, d time.Duration, j int, err error) baris {
		b := baris{Nama: nama, TotalMs: ms(d), PerCallUs: float64(d.Nanoseconds()) / 1e3 / float64(p.N), Jumlah: j}
		if err != nil {
			b.Error = err.Error()
		}
		return b
	}
	tulisJSON(w, 200, map[string]any{
		"n": p.N,
		"hasil": []baris{
			mk("monolith: panggilan fungsi lokal", lokal, jumlahLokal, nil),
			mk("terdistribusi: HTTP langsung ke "+target.Nama, langsung, jumlahLangsung, err1),
			mk("terdistribusi: HTTP lewat nginx", viaLB, jumlahLB, err2),
		},
	})
}

func (m *monitor) nodePertamaSehat() nodeInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.nodes {
		if m.state[n.Nama].Status == StatusHidup {
			return n
		}
	}
	return m.nodes[0]
}

func tulisJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
