// clocknode: satu proses dalam demo Lamport clock dan vector clock.
// Setiap node memegang Lamport clock, vector clock, dan jam dinding yang sengaja digeser
// (CLOCK_SKEW_MS) untuk memperlihatkan bahwa jam fisik tiap mesin "berbohong sedikit-sedikit".
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/clock"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

// Event adalah satu kejadian di garis waktu proses ini.
type Event struct {
	ID      string       `json:"id"`
	Proc    string       `json:"proc"`
	Seq     int          `json:"seq"`
	Kind    string       `json:"kind"` // local | send | recv
	Lamport int          `json:"lamport"`
	Vector  clock.Vector `json:"vector"`
	MsgID   string       `json:"msg_id,omitempty"`
	Peer    string       `json:"peer,omitempty"`
	Wall    int64        `json:"wall_ms"` // jam dinding node (sudah termasuk skew)
	Real    int64        `json:"real_ms"` // jam host sebenarnya, hanya untuk pembanding di UI
}

// Message adalah isi pesan antarnode: cap Lamport dan vector clock pengirim.
type Message struct {
	MsgID   string       `json:"msg_id"`
	From    string       `json:"from"`
	Lamport int          `json:"lamport"`
	Vector  clock.Vector `json:"vector"`
}

type node struct {
	mu     sync.Mutex
	id     string
	index  int
	names  []string
	peers  map[string]string
	skew   time.Duration
	lam    clock.Lamport
	vec    clock.Vector
	events []Event
	seq    int
	log    *slog.Logger
}

func (n *node) record(kind, msgID, peer string) Event {
	n.seq++
	now := time.Now()
	e := Event{
		ID: fmt.Sprintf("%s-%d", n.id, n.seq), Proc: n.id, Seq: n.seq, Kind: kind,
		Lamport: n.lam.C, Vector: n.vec.Copy(), MsgID: msgID, Peer: peer,
		Wall: now.Add(n.skew).UnixMilli(), Real: now.UnixMilli(),
	}
	n.events = append(n.events, e)
	if len(n.events) > 200 {
		n.events = n.events[len(n.events)-200:]
	}
	n.log.Info("event", "kind", kind, "lamport", e.Lamport, "vector", fmt.Sprint(e.Vector), "peer", peer, "msg_id", msgID)
	return e
}

func (n *node) local(w http.ResponseWriter, _ *http.Request) {
	n.mu.Lock()
	n.lam.Local()
	n.vec.Tick(n.index)
	e := n.record("local", "", "")
	n.mu.Unlock()
	httpx.JSON(w, 200, e)
}

func (n *node) send(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	url, ok := n.peers[to]
	if !ok || to == n.id {
		httpx.Err(w, 400, "tujuan tidak dikenal: "+to)
		return
	}
	delay, _ := strconv.Atoi(r.URL.Query().Get("delay_ms"))
	n.mu.Lock()
	n.lam.Send()
	n.vec.Tick(n.index)
	msgID := fmt.Sprintf("m-%s-%d", n.id, n.seq+1)
	e := n.record("send", msgID, to)
	msg := Message{MsgID: msgID, From: n.id, Lamport: e.Lamport, Vector: e.Vector}
	n.mu.Unlock()

	deliver := func() error {
		b, _ := json.Marshal(msg)
		resp, err := http.Post(url+"/receive", "application/json", bytes.NewReader(b))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	if delay <= 0 {
		// Tanpa delay: kirim sinkron agar skenario berurutan di UI deterministik.
		if err := deliver(); err != nil {
			httpx.Err(w, 502, err.Error())
			return
		}
	} else {
		// Dengan delay: pesan "di jalan", sehingga dua pesan bisa bersilangan.
		go func() {
			time.Sleep(time.Duration(delay) * time.Millisecond)
			if err := deliver(); err != nil {
				n.log.Error("gagal kirim", "to", to, "err", err.Error())
			}
		}()
	}
	httpx.JSON(w, 200, e)
}

func (n *node) receive(w http.ResponseWriter, r *http.Request) {
	var m Message
	if err := httpx.Decode(r, &m); err != nil {
		httpx.Err(w, 400, err.Error())
		return
	}
	n.mu.Lock()
	n.lam.Recv(m.Lamport)
	n.vec.Merge(m.Vector, n.index)
	e := n.record("recv", m.MsgID, m.From)
	n.mu.Unlock()
	httpx.JSON(w, 200, e)
}

func (n *node) state(w http.ResponseWriter, _ *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	httpx.JSON(w, 200, map[string]any{
		"node": n.id, "index": n.index, "procs": n.names, "skew_ms": n.skew.Milliseconds(),
		"lamport": n.lam.C, "vector": n.vec, "events": n.events,
	})
}

func (n *node) reset(w http.ResponseWriter, _ *http.Request) {
	n.mu.Lock()
	n.lam = clock.Lamport{}
	n.vec = clock.NewVector(len(n.names))
	n.events = []Event{}
	n.seq = 0
	n.mu.Unlock()
	n.log.Info("reset")
	httpx.JSON(w, 200, map[string]string{"status": "ok"})
}

func main() {
	id := httpx.Env("NODE_ID", "P1")
	log := logx.New("clock-" + strings.ToLower(id))
	// PEERS = "P1=http://clock-p1:8080,P2=...,P3=..."; urutan menentukan indeks vektor.
	peers := map[string]string{}
	var names []string
	for _, p := range strings.Split(httpx.Env("PEERS", "P1=http://localhost:8080"), ",") {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) == 2 {
			peers[kv[0]] = kv[1]
			names = append(names, kv[0])
		}
	}
	idx := -1
	for i, nm := range names {
		if nm == id {
			idx = i
		}
	}
	if idx < 0 {
		log.Error("NODE_ID tidak ada di PEERS")
		return
	}
	skew, _ := strconv.Atoi(httpx.Env("CLOCK_SKEW_MS", "0"))
	n := &node{id: id, index: idx, names: names, peers: peers, skew: time.Duration(skew) * time.Millisecond,
		vec: clock.NewVector(len(names)), events: []Event{}, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /local", n.local)
	mux.HandleFunc("POST /send", n.send)
	mux.HandleFunc("POST /receive", n.receive)
	mux.HandleFunc("POST /reset", n.reset)
	mux.HandleFunc("GET /state", n.state)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	addr := ":" + httpx.Env("PORT", "8080")
	log.Info("mulai", "addr", addr, "index", idx, "skew_ms", skew)
	if err := http.ListenAndServe(addr, httpx.Log(log, mux)); err != nil {
		log.Error("berhenti", "err", err.Error())
	}
}
