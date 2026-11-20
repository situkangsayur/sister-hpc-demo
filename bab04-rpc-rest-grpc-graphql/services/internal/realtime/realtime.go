// Paket realtime mengirim data ke klien tanpa ditanya (Subbab Real-time ke
// Klien): polling biasa (cukup GET REST), long polling, SSE, dan WebSocket.
//
// Koneksi SSE dan WebSocket menempel pada satu replica. Agar perubahan yang
// terjadi di replica lain tetap sampai, setiap replica berlangganan channel
// Postgres LISTEN/NOTIFY (peran pub-sub pada Gambar b04-ws di buku).
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/lib/pq"

	"sister/bab04/internal/toko"
)

// Pesan adalah satu event yang diterima dari pub-sub.
type Pesan struct {
	Saluran string
	Isi     string
}

// Hub menyebarkan pesan pub-sub ke semua pelanggan lokal di replica ini.
type Hub struct {
	Node string
	Toko *toko.Toko

	mu        sync.Mutex
	pelanggan map[chan Pesan]struct{}
	riwayat   []toko.PerubahanHarga // 100 perubahan harga terakhir untuk long polling
	baru      chan struct{}         // ditutup lalu diganti setiap ada perubahan harga

	tickerMu    sync.Mutex
	tickerBatal context.CancelFunc
	tickerMs    int
}

// BaruHub membuat hub dan mulai mendengarkan Postgres.
func BaruHub(ctx context.Context, url, node string, t *toko.Toko) *Hub {
	h := &Hub{Node: node, Toko: t, pelanggan: map[chan Pesan]struct{}{}, baru: make(chan struct{})}
	l := pq.NewListener(url, time.Second, 10*time.Second, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			slog.Warn("listener postgres", "err", err)
		}
	})
	for _, s := range []string{toko.SaluranHarga, toko.SaluranObrolan} {
		if err := l.Listen(s); err != nil {
			slog.Error("LISTEN gagal", "saluran", s, "err", err)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				l.Close()
				return
			case n := <-l.Notify:
				if n != nil {
					h.sebar(Pesan{Saluran: n.Channel, Isi: n.Extra})
				}
			case <-time.After(60 * time.Second):
				go l.Ping()
			}
		}
	}()
	return h
}

func (h *Hub) sebar(p Pesan) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.Saluran == toko.SaluranHarga {
		var ph toko.PerubahanHarga
		if json.Unmarshal([]byte(p.Isi), &ph) == nil {
			h.riwayat = append(h.riwayat, ph)
			if len(h.riwayat) > 100 {
				h.riwayat = h.riwayat[len(h.riwayat)-100:]
			}
			close(h.baru)
			h.baru = make(chan struct{})
		}
	}
	for c := range h.pelanggan {
		select {
		case c <- p:
		default:
			// Backpressure: klien lambat tidak boleh membuat antrean tumbuh tanpa
			// batas. Pesan untuk klien ini dibuang.
		}
	}
}

// Langganan mendaftarkan pelanggan baru dengan buffer terbatas.
func (h *Hub) Langganan() (chan Pesan, func()) {
	c := make(chan Pesan, 32)
	h.mu.Lock()
	h.pelanggan[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.pelanggan, c)
		h.mu.Unlock()
	}
}

// JumlahPelanggan dipakai UI untuk memperlihatkan koneksi stateful per replica.
func (h *Hub) JumlahPelanggan() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pelanggan)
}

// Daftarkan memasang route real-time.
func (h *Hub) Daftarkan(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/harga/stream", h.sse)
	mux.HandleFunc("GET /v1/harga/tunggu", h.longPoll)
	mux.HandleFunc("GET /v1/chat", h.chat)
	mux.HandleFunc("GET /admin/ticker", h.statusTicker)
	mux.HandleFunc("PUT /admin/ticker", h.aturTicker)
}

func cocok(ids map[int64]bool, id int64) bool { return len(ids) == 0 || ids[id] }

func bacaIDs(s string) map[int64]bool {
	ids := map[int64]bool{}
	for _, b := range strings.Split(s, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64); err == nil {
			ids[n] = true
		}
	}
	return ids
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

// sse: satu response HTTP yang tidak pernah selesai, berisi baris "data: ...".
func (h *Hub) sse(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming tidak didukung", http.StatusInternalServerError)
		return
	}
	cors(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // minta gateway (nginx/APISIX) tidak mem-buffer
	ids := bacaIDs(r.URL.Query().Get("id"))
	c, lepas := h.Langganan()
	defer lepas()
	slog.InfoContext(r.Context(), "sse tersambung", "pelanggan", h.JumlahPelanggan())
	// retry: browser menyambung ulang otomatis setelah 2 detik bila putus
	w.Write([]byte("retry: 2000\nevent: halo\ndata: {\"node\":\"" + h.Node + "\"}\n\n"))
	f.Flush()
	detak := time.NewTicker(15 * time.Second)
	defer detak.Stop()
	for {
		select {
		case <-r.Context().Done(): // klien pergi: berhenti
			return
		case <-detak.C:
			w.Write([]byte(": ping\n\n")) // heartbeat agar proxy tidak memutus koneksi diam
			f.Flush()
		case p := <-c:
			if p.Saluran != toko.SaluranHarga {
				continue
			}
			var ph toko.PerubahanHarga
			if json.Unmarshal([]byte(p.Isi), &ph) != nil || !cocok(ids, ph.ID) {
				continue
			}
			isi, _ := json.Marshal(map[string]any{"perubahan": ph, "node": h.Node})
			w.Write([]byte("event: harga\ndata: " + string(isi) + "\n\n"))
			f.Flush()
		}
	}
}

// longPoll menahan request sampai ada perubahan harga sesudah ?setelah=<ms>,
// paling lama 25 detik (lalu 204 dan klien bertanya lagi).
func (h *Hub) longPoll(w http.ResponseWriter, r *http.Request) {
	cors(w)
	setelah, _ := strconv.ParseInt(r.URL.Query().Get("setelah"), 10, 64)
	ids := bacaIDs(r.URL.Query().Get("id"))
	batas := time.After(25 * time.Second)
	for {
		h.mu.Lock()
		var hasil []toko.PerubahanHarga
		for _, ph := range h.riwayat {
			if ph.WaktuMs > setelah && cocok(ids, ph.ID) {
				hasil = append(hasil, ph)
			}
		}
		tunggu := h.baru
		h.mu.Unlock()
		if len(hasil) > 0 || setelah == 0 {
			w.Header().Set("Content-Type", "application/json")
			if hasil == nil {
				hasil = []toko.PerubahanHarga{}
			}
			json.NewEncoder(w).Encode(map[string]any{"perubahan": hasil, "node": h.Node, "sekarang_ms": time.Now().UnixMilli()})
			return
		}
		select {
		case <-tunggu:
		case <-batas:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// pesanChat adalah isi pesan chat WebSocket.
type pesanChat struct {
	Nama    string `json:"nama"`
	Teks    string `json:"teks"`
	Node    string `json:"node"`
	WaktuMs int64  `json:"waktu_ms"`
	Jenis   string `json:"jenis"` // chat, masuk, keluar
}

// chat: request HTTP dengan Upgrade: websocket, dijawab 101 Switching Protocols,
// lalu menjadi channel dua arah. Pesan dari satu klien diumumkan lewat NOTIFY
// sehingga klien di replica lain juga menerimanya.
func (h *Hub) chat(w http.ResponseWriter, r *http.Request) {
	nama := strings.TrimSpace(r.URL.Query().Get("nama"))
	if nama == "" {
		nama = "tamu"
	}
	if len(nama) > 24 {
		nama = nama[:24]
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // demo: izinkan origin UI
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(4096)
	ctx, batal := context.WithCancel(r.Context())
	defer batal()
	c, lepas := h.Langganan()
	defer lepas()

	umumkan := func(jenis, teks string) {
		isi, _ := json.Marshal(pesanChat{Nama: nama, Teks: teks, Node: h.Node, WaktuMs: time.Now().UnixMilli(), Jenis: jenis})
		if err := h.Toko.Umumkan(context.WithoutCancel(ctx), toko.SaluranObrolan, string(isi)); err != nil {
			slog.Warn("umumkan chat", "err", err)
		}
	}
	umumkan("masuk", "")
	defer umumkan("keluar", "")
	slog.InfoContext(ctx, "websocket tersambung", "nama", nama, "pelanggan", h.JumlahPelanggan())

	// goroutine baca: pesan dari klien diteruskan ke pub-sub
	go func() {
		defer batal()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Teks string `json:"teks"`
			}
			if json.Unmarshal(data, &m) == nil && strings.TrimSpace(m.Teks) != "" {
				t := m.Teks
				if len(t) > 500 {
					t = t[:500]
				}
				umumkan("chat", t)
			}
		}
	}()
	detak := time.NewTicker(20 * time.Second)
	defer detak.Stop()
	for {
		select {
		case <-ctx.Done():
			ws.Close(websocket.StatusNormalClosure, "")
			return
		case <-detak.C:
			pc, b := context.WithTimeout(ctx, 5*time.Second)
			err := ws.Ping(pc) // heartbeat
			b()
			if err != nil {
				return
			}
		case p := <-c:
			if p.Saluran != toko.SaluranObrolan {
				continue
			}
			// tambahkan node penerima agar terlihat pesan menyeberang antarreplica
			var m map[string]any
			if json.Unmarshal([]byte(p.Isi), &m) != nil {
				continue
			}
			m["node_penerima"] = h.Node
			isi, _ := json.Marshal(m)
			wc, b := context.WithTimeout(ctx, 5*time.Second)
			err := ws.Write(wc, websocket.MessageText, isi)
			b()
			if err != nil {
				return
			}
		}
	}
}

func (h *Hub) statusTicker(w http.ResponseWriter, _ *http.Request) {
	h.tickerMu.Lock()
	aktif, ms := h.tickerBatal != nil, h.tickerMs
	h.tickerMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"aktif": aktif, "interval_ms": ms, "node": h.Node})
}

// aturTicker menyalakan/mematikan pengubah harga otomatis agar stream SSE ramai.
func (h *Hub) aturTicker(w http.ResponseWriter, r *http.Request) {
	var a struct {
		Aktif      bool `json:"aktif"`
		IntervalMs int  `json:"interval_ms"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&a) != nil {
		http.Error(w, "json tidak sah", http.StatusBadRequest)
		return
	}
	if a.IntervalMs < 200 {
		a.IntervalMs = 1000
	}
	h.tickerMu.Lock()
	if h.tickerBatal != nil {
		h.tickerBatal()
		h.tickerBatal = nil
	}
	if a.Aktif {
		ctx, batal := context.WithCancel(context.Background())
		h.tickerBatal, h.tickerMs = batal, a.IntervalMs
		go h.jalankanTicker(ctx, time.Duration(a.IntervalMs)*time.Millisecond)
	}
	h.tickerMu.Unlock()
	h.statusTicker(w, r)
}

func (h *Hub) jalankanTicker(ctx context.Context, iv time.Duration) {
	t := time.NewTicker(iv)
	defer t.Stop()
	ids := []int64{42, 1, 2, 3}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			id := ids[rand.IntN(len(ids))]
			c, b := context.WithTimeout(toko.DenganOleh(ctx, "ticker"), 2*time.Second)
			p, err := h.Toko.Ambil(c, id)
			if err == nil {
				baru := p.Harga + int64(rand.IntN(5)-2)*500
				if baru < 1000 {
					baru = 1000
				}
				_, err = h.Toko.UbahHarga(c, id, baru)
			}
			b()
			if err != nil {
				slog.Warn("ticker", "err", err)
			}
		}
	}
}
