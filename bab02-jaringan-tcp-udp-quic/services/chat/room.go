package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ClientInfo adalah data client yang ditampilkan di UI.
type ClientInfo struct {
	ID       int       `json:"id"`
	Nick     string    `json:"nick"`
	Kind     string    `json:"kind"`   // tcp (mis. nc) | websocket (lewat gateway)
	Remote   string    `json:"remote"` // alamat socket yang dilihat server
	Origin   string    `json:"origin,omitempty"`
	JoinedAt time.Time `json:"joined_at"`
	Msgs     int       `json:"msgs"`
}

// Client adalah satu socket yang terhubung ke ruang chat.
type Client struct {
	ClientInfo
	out  chan string
	quit chan struct{}
	once sync.Once
}

func (c *Client) close() { c.once.Do(func() { close(c.quit) }) }

// Message adalah satu baris di riwayat ruang.
type Message struct {
	TS   time.Time `json:"ts"`
	From string    `json:"from"`
	Kind string    `json:"kind"` // chat | system
	Text string    `json:"text"`
	Via  string    `json:"via,omitempty"`
}

// Room menyimpan semua client dan riwayat pesan.
type Room struct {
	mu      sync.Mutex
	clients map[int]*Client
	history []Message
	nextID  int
	now     func() time.Time
}

// NewRoom membuat ruang kosong.
func NewRoom() *Room { return &Room{clients: map[int]*Client{}, now: time.Now} }

var reNick = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,20}$`)

// Join mendaftarkan socket baru dengan nick sementara tamu-N.
func (r *Room) Join(remote string) *Client {
	r.mu.Lock()
	r.nextID++
	c := &Client{ClientInfo: ClientInfo{ID: r.nextID, Nick: fmt.Sprintf("tamu-%d", r.nextID), Kind: "tcp",
		Remote: remote, JoinedAt: r.now()}, out: make(chan string, 64), quit: make(chan struct{})}
	r.clients[c.ID] = c
	r.mu.Unlock()
	c.send("*** Selamat datang di ruang chat Bab 2 (socket TCP mentah, protokol per baris).")
	c.send("*** Perintah: /nick <nama>, /who, /quit. Baris lain dikirim ke semua orang.")
	r.system(fmt.Sprintf("%s bergabung dari %s", c.Nick, remote))
	return c
}

// Leave mengeluarkan client.
func (r *Room) Leave(c *Client) {
	r.mu.Lock()
	_, ok := r.clients[c.ID]
	delete(r.clients, c.ID)
	r.mu.Unlock()
	c.close()
	if ok {
		r.system(fmt.Sprintf("%s keluar", c.Nick))
	}
}

// send menaruh baris di antrean tulis client. Antrean penuh = client terlalu
// lambat; ia diputus agar tidak menahan client lain.
func (c *Client) send(line string) bool {
	select {
	case c.out <- line:
		return true
	default:
		c.close()
		return false
	}
}

func (r *Room) record(m Message) {
	r.history = append(r.history, m)
	if len(r.history) > 100 {
		r.history = r.history[len(r.history)-100:]
	}
}

func (r *Room) broadcast(line string) {
	for _, c := range r.clients {
		c.send(line)
	}
}

func (r *Room) system(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(Message{TS: r.now(), From: "server", Kind: "system", Text: text})
	r.broadcast("*** " + text)
}

// Handle memproses satu baris dari client. Mengembalikan false bila client ingin keluar.
func (r *Room) Handle(c *Client, line string) bool {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return true
	}
	if strings.HasPrefix(line, "/") {
		f := strings.Fields(line)
		switch f[0] {
		case "/quit":
			c.send("*** sampai jumpa")
			return false
		case "/who":
			for _, x := range r.Clients() {
				c.send(fmt.Sprintf("*** %s (%s, %s)", x.Nick, x.Kind, x.Remote))
			}
		case "/nick":
			if len(f) < 2 || !reNick.MatchString(f[1]) {
				c.send("*** nick harus 1-20 karakter huruf, angka, _ . -")
				return true
			}
			r.mu.Lock()
			for _, x := range r.clients {
				if x.ID != c.ID && strings.EqualFold(x.Nick, f[1]) {
					r.mu.Unlock()
					c.send("*** nick " + f[1] + " sudah dipakai")
					return true
				}
			}
			old := c.Nick
			c.Nick = f[1]
			r.mu.Unlock()
			r.system(fmt.Sprintf("%s sekarang bernama %s", old, f[1]))
		case "/via":
			// dikirim gateway WebSocket sebagai baris pertama: "/via websocket <alamat browser>"
			if len(f) >= 2 && f[1] == "websocket" {
				r.mu.Lock()
				c.Kind = "websocket"
				if len(f) >= 3 {
					c.Origin = f[2]
				}
				r.mu.Unlock()
			}
		default:
			c.send("*** perintah tidak dikenal: " + f[0])
		}
		return true
	}
	if len(line) > 500 {
		line = line[:500]
	}
	r.mu.Lock()
	c.Msgs++
	ts := r.now()
	r.record(Message{TS: ts, From: c.Nick, Kind: "chat", Text: line, Via: c.Kind})
	r.broadcast(fmt.Sprintf("[%s] %s: %s", ts.Format("15:04:05"), c.Nick, line))
	r.mu.Unlock()
	return true
}

// Clients mengembalikan salinan daftar client, urut menurut ID.
func (r *Room) Clients() []ClientInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ClientInfo, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c.ClientInfo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// History mengembalikan salinan riwayat.
func (r *Room) History() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message{}, r.history...)
}
