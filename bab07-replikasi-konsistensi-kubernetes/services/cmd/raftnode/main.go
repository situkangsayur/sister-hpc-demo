// raftnode: satu node kluster Raft 3 node (hashicorp/raft) yang mereplikasi key-value store.
// Endpoint /admin/crash mensimulasikan crash (raft.Shutdown, port Raft tertutup, state di memori
// hilang); /admin/restart menghidupkan lagi dari log dan stable store yang tersimpan di disk.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/kv"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

type peer struct {
	ID       string `json:"id"`
	RaftAddr string `json:"raft_addr"`
	HTTPAddr string `json:"http_addr"`
}

// Transition adalah perubahan peran node (follower, candidate, leader, shutdown).
type Transition struct {
	At    int64  `json:"at_ms"`
	State string `json:"state"`
	Term  uint64 `json:"term"`
}

type node struct {
	mu      sync.Mutex
	id      string
	addr    string
	dir     string
	peers   []peer
	r       *raft.Raft
	store   *raftboltdb.BoltStore
	fsm     *kv.FSM
	obsCh   chan raft.Observation
	obs     *raft.Observer
	history []Transition
	log     *slog.Logger
}

func (n *node) addHistory(state string, term uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.history = append(n.history, Transition{At: time.Now().UnixMilli(), State: state, Term: term})
	if len(n.history) > 60 {
		n.history = n.history[len(n.history)-60:]
	}
}

func (n *node) start() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.r != nil {
		return errors.New("node sudah berjalan")
	}
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(n.id)
	// Timeout dibuat sedang agar transisi follower -> candidate -> leader sempat terlihat di UI.
	cfg.HeartbeatTimeout = 1000 * time.Millisecond
	cfg.ElectionTimeout = 1000 * time.Millisecond
	cfg.LeaderLeaseTimeout = 500 * time.Millisecond
	cfg.CommitTimeout = 50 * time.Millisecond
	cfg.SnapshotThreshold = 1 << 20 // praktis tanpa snapshot agar seluruh log tetap terlihat
	cfg.Logger = hclog.New(&hclog.LoggerOptions{Name: "raft-" + n.id, Output: os.Stdout, JSONFormat: true, Level: hclog.Info})

	store, err := raftboltdb.New(raftboltdb.Options{Path: filepath.Join(n.dir, "raft.db")})
	if err != nil {
		return err
	}
	snaps, err := raft.NewFileSnapshotStore(n.dir, 2, io.Discard)
	if err != nil {
		store.Close()
		return err
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", n.addr)
	if err != nil {
		store.Close()
		return err
	}
	bind := fmt.Sprintf("0.0.0.0:%d", tcpAddr.Port)
	trans, err := raft.NewTCPTransport(bind, tcpAddr, 3, 2*time.Second, io.Discard)
	if err != nil {
		store.Close()
		return err
	}
	fsm := kv.New()
	r, err := raft.NewRaft(cfg, fsm, store, store, snaps, trans)
	if err != nil {
		trans.Close()
		store.Close()
		return err
	}
	has, _ := raft.HasExistingState(store, store, snaps)
	if !has {
		// Semua node melakukan bootstrap dengan konfigurasi identik; ini aman di hashicorp/raft.
		var servers []raft.Server
		for _, p := range n.peers {
			servers = append(servers, raft.Server{ID: raft.ServerID(p.ID), Address: raft.ServerAddress(p.RaftAddr)})
		}
		if err := r.BootstrapCluster(raft.Configuration{Servers: servers}).Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			n.log.Warn("bootstrap", "err", err.Error())
		}
	}
	n.obsCh = make(chan raft.Observation, 64)
	obs := raft.NewObserver(n.obsCh, false, func(o *raft.Observation) bool {
		_, ok := o.Data.(raft.RaftState)
		return ok
	})
	r.RegisterObserver(obs)
	go func(ch chan raft.Observation, r *raft.Raft) {
		for o := range ch {
			st := o.Data.(raft.RaftState)
			term, _ := strconv.ParseUint(r.Stats()["term"], 10, 64)
			n.addHistory(strings.ToLower(st.String()), term)
			n.log.Info("state", "state", st.String(), "term", term)
		}
	}(n.obsCh, r)
	n.r, n.store, n.fsm, n.obs = r, store, fsm, obs
	n.history = append(n.history, Transition{At: time.Now().UnixMilli(), State: "start", Term: 0})
	n.log.Info("raft mulai", "addr", n.addr, "existing_state", has)
	return nil
}

func (n *node) crash() error {
	n.mu.Lock()
	r, store, ch, obs := n.r, n.store, n.obsCh, n.obs
	n.r, n.store, n.fsm, n.obs = nil, nil, nil, nil
	n.mu.Unlock()
	if r == nil {
		return errors.New("node sudah mati")
	}
	err := r.Shutdown().Error()
	r.DeregisterObserver(obs)
	store.Close()
	close(ch)
	n.addHistory("crash", 0)
	n.log.Warn("node di-crash (simulasi)")
	return err
}

// LogEntry adalah tampilan satu entri replicated log.
type LogEntry struct {
	Index uint64 `json:"index"`
	Term  uint64 `json:"term"`
	Type  string `json:"type"`
	Data  string `json:"data"`
}

func (n *node) status(w http.ResponseWriter, _ *http.Request) {
	n.mu.Lock()
	r, store, fsm := n.r, n.store, n.fsm
	hist := append([]Transition(nil), n.history...)
	n.mu.Unlock()
	out := map[string]any{"id": n.id, "raft_addr": n.addr, "peers": n.peers, "history": hist, "alive": r != nil}
	if r == nil {
		httpx.JSON(w, 200, out)
		return
	}
	st := r.Stats()
	leaderAddr, leaderID := r.LeaderWithID()
	out["state"] = strings.ToLower(r.State().String())
	out["term"] = st["term"]
	out["leader_id"] = string(leaderID)
	out["leader_addr"] = string(leaderAddr)
	out["commit_index"] = st["commit_index"]
	out["applied_index"] = st["applied_index"]
	out["last_log_index"] = st["last_log_index"]
	out["last_contact"] = st["last_contact"]
	out["kv"] = fsm.Data()
	first, _ := store.FirstIndex()
	last, _ := store.LastIndex()
	if last > 40 && first < last-39 {
		first = last - 39
	}
	var entries []LogEntry
	for i := first; i <= last && i > 0; i++ {
		var l raft.Log
		if err := store.GetLog(i, &l); err != nil {
			continue
		}
		e := LogEntry{Index: l.Index, Term: l.Term, Type: l.Type.String()}
		if l.Type == raft.LogCommand {
			e.Data = string(l.Data)
		}
		entries = append(entries, e)
	}
	out["log"] = entries
	httpx.JSON(w, 200, out)
}

// httpOf mencari alamat HTTP leader dari ID-nya. Alamat Raft tidak dipakai karena di Docker
// transport mengiklankan IP container, bukan nama service.
func (n *node) httpOf(id string) string {
	for _, p := range n.peers {
		if p.ID == id {
			return p.HTTPAddr
		}
	}
	return ""
}

func (n *node) set(w http.ResponseWriter, req *http.Request) {
	var c kv.Command
	if err := httpx.Decode(req, &c); err != nil || c.Key == "" {
		httpx.Err(w, 400, "body harus {key, value}")
		return
	}
	if c.Op == "" {
		c.Op = "set"
	}
	n.mu.Lock()
	r := n.r
	n.mu.Unlock()
	if r == nil {
		httpx.Err(w, 503, "node ini sedang mati")
		return
	}
	if r.State() != raft.Leader {
		// Hanya leader yang boleh menambah log; follower meneruskan ke leader.
		_, leaderID := r.LeaderWithID()
		h := n.httpOf(string(leaderID))
		if h == "" || req.Header.Get("X-Forwarded-By") != "" {
			httpx.Err(w, 503, "belum ada leader (election sedang berlangsung?)")
			return
		}
		b, _ := json.Marshal(c)
		fr, _ := http.NewRequest(http.MethodPost, h+"/set", bytes.NewReader(b))
		fr.Header.Set("Content-Type", "application/json")
		fr.Header.Set("X-Forwarded-By", n.id)
		resp, err := http.DefaultClient.Do(fr)
		if err != nil {
			httpx.Err(w, 502, err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	b, _ := json.Marshal(c)
	start := time.Now()
	f := r.Apply(b, 3*time.Second)
	if err := f.Error(); err != nil {
		httpx.Err(w, 503, "apply gagal: "+err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"index": f.Index(), "leader": n.id, "via": req.Header.Get("X-Forwarded-By"),
		"commit_ms": time.Since(start).Milliseconds()})
}

func main() {
	id := httpx.Env("NODE_ID", "n1")
	log := logx.New("raft-" + id)
	// PEERS = "n1=raft-n1:7000=http://raft-n1:8080,..."
	var peers []peer
	for _, p := range strings.Split(httpx.Env("PEERS", ""), ",") {
		f := strings.SplitN(strings.TrimSpace(p), "=", 3)
		if len(f) == 3 {
			peers = append(peers, peer{ID: f[0], RaftAddr: f[1], HTTPAddr: f[2]})
		}
	}
	n := &node{id: id, dir: httpx.Env("DATA_DIR", "/data"), peers: peers, log: log}
	for _, p := range peers {
		if p.ID == id {
			n.addr = p.RaftAddr
		}
	}
	if n.addr == "" {
		log.Error("NODE_ID tidak ada di PEERS")
		os.Exit(1)
	}
	if err := os.MkdirAll(n.dir, 0o755); err != nil {
		log.Error("data dir", "err", err.Error())
		os.Exit(1)
	}
	if err := n.start(); err != nil {
		log.Error("start", "err", err.Error())
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", n.status)
	mux.HandleFunc("POST /set", n.set)
	mux.HandleFunc("POST /admin/crash", func(w http.ResponseWriter, _ *http.Request) {
		if err := n.crash(); err != nil {
			httpx.Err(w, 409, err.Error())
			return
		}
		httpx.JSON(w, 200, map[string]string{"status": "crashed"})
	})
	mux.HandleFunc("POST /admin/restart", func(w http.ResponseWriter, _ *http.Request) {
		if err := n.start(); err != nil {
			httpx.Err(w, 409, err.Error())
			return
		}
		httpx.JSON(w, 200, map[string]string{"status": "started"})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	addr := ":" + httpx.Env("PORT", "8080")
	log.Info("http mulai", "addr", addr)
	if err := http.ListenAndServe(addr, httpx.Log(log, mux)); err != nil {
		log.Error("berhenti", "err", err.Error())
	}
}
