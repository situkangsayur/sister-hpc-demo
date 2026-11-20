// searchapi: klien Go untuk kluster OpenSearch 2 node. Membuat index ber-shard dan ber-replica,
// memantau alokasi shard per node, dan mencatat kejadian penting: node keluar/masuk,
// status kluster berubah, dan replica yang dipromosikan menjadi primary.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab07/services/internal/logx"
)

const index = "katalog"

// Shard adalah satu baris _cat/shards.
type Shard struct {
	Shard  string `json:"shard"`
	Prirep string `json:"prirep"`
	State  string `json:"state"`
	Node   string `json:"node"`
	Docs   string `json:"docs"`
	Reason string `json:"unassigned.reason"`
}

// Node adalah satu baris _cat/nodes.
type Node struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Role    string `json:"node.role"`
	Manager string `json:"cluster_manager"`
	Heap    string `json:"heap.percent"`
}

// Event adalah kejadian yang terdeteksi dari perubahan state kluster.
type Event struct {
	At   int64  `json:"at_ms"`
	Kind string `json:"kind"`
	Msg  string `json:"msg"`
}

// Snapshot adalah keadaan kluster terakhir yang diamati.
type Snapshot struct {
	At      int64           `json:"at_ms"`
	Up      bool            `json:"up"`
	Via     string          `json:"via"`
	Health  json.RawMessage `json:"health"`
	Status  string          `json:"status"`
	Nodes   []Node          `json:"nodes"`
	Shards  []Shard         `json:"shards"`
	DocCnt  int             `json:"doc_count"`
	Error   string          `json:"error,omitempty"`
	Known   []string        `json:"known_nodes"`
	History []HealthPoint   `json:"history"`
	Events  []Event         `json:"events"`
}

// HealthPoint adalah status kluster pada satu waktu (untuk pita warna di UI).
type HealthPoint struct {
	At     int64  `json:"at_ms"`
	Status string `json:"status"`
}

type app struct {
	urls   []string
	known  []string
	client *http.Client
	log    *slog.Logger

	mu      sync.Mutex
	last    Snapshot
	events  []Event
	history []HealthPoint
	prim    map[string]string // shard -> node primary terakhir
	nodes   map[string]bool
	status  string
	ready   bool
}

// do mencoba setiap node bergantian sampai satu menjawab.
func (a *app) do(ctx context.Context, method, path string, body []byte) ([]byte, string, error) {
	var lastErr error
	for _, u := range a.urls {
		req, _ := http.NewRequestWithContext(ctx, method, u+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if strings.HasSuffix(path, "_bulk") {
			req.Header.Set("Content-Type", "application/x-ndjson")
		}
		resp, err := a.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: HTTP %d %s", u, resp.StatusCode, truncate(string(b), 200))
			continue
		}
		if resp.StatusCode >= 400 {
			return b, u, fmt.Errorf("HTTP %d %s", resp.StatusCode, truncate(string(b), 200))
		}
		return b, u, nil
	}
	return nil, "", lastErr
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func (a *app) addEvent(kind, msg string) {
	a.events = append(a.events, Event{At: time.Now().UnixMilli(), Kind: kind, Msg: msg})
	if len(a.events) > 50 {
		a.events = a.events[len(a.events)-50:]
	}
	a.log.Info("cluster event", "kind", kind, "msg", msg)
}

func (a *app) setup(ctx context.Context) error {
	if _, _, err := a.do(ctx, http.MethodGet, "/_cluster/health?wait_for_status=yellow&timeout=5s", nil); err != nil {
		return err
	}
	if _, _, err := a.do(ctx, http.MethodHead, "/"+index, nil); err == nil {
		return nil
	}
	settings := `{"settings":{"number_of_shards":3,"number_of_replicas":1,
	  "index.unassigned.node_left.delayed_timeout":"5s"},
	  "mappings":{"properties":{"nama":{"type":"text"},"kategori":{"type":"keyword"},"harga":{"type":"integer"}}}}`
	if _, _, err := a.do(ctx, http.MethodPut, "/"+index, []byte(settings)); err != nil && !strings.Contains(err.Error(), "already_exists") {
		return err
	}
	var bulk strings.Builder
	barang := []string{"kopi", "teh", "gula", "beras", "minyak", "susu", "roti", "mie", "kecap", "sambal"}
	kat := []string{"minuman", "minuman", "bahan", "bahan", "bahan", "minuman", "makanan", "makanan", "bumbu", "bumbu"}
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&bulk, "{\"index\":{\"_index\":\"%s\",\"_id\":\"%d\"}}\n", index, i+1)
		fmt.Fprintf(&bulk, "{\"nama\":\"%s varian %d\",\"kategori\":\"%s\",\"harga\":%d}\n", barang[i%10], i/10+1, kat[i%10], 5000+i*250)
	}
	if _, _, err := a.do(ctx, http.MethodPost, "/_bulk?refresh=true", []byte(bulk.String())); err != nil {
		return err
	}
	a.mu.Lock()
	a.addEvent("setup", "index katalog dibuat: 3 primary shard, 1 replica per shard, 60 dokumen")
	a.mu.Unlock()
	return nil
}

func (a *app) observe(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s := Snapshot{At: time.Now().UnixMilli(), Known: a.known}
	hb, via, err := a.do(ctx, http.MethodGet, "/_cluster/health", nil)
	if err != nil {
		s.Error = err.Error()
		s.Status = "unavailable"
	} else {
		s.Up, s.Via, s.Health = true, via, hb
		var h struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(hb, &h)
		s.Status = h.Status
		if b, _, err := a.do(ctx, http.MethodGet, "/_cat/nodes?format=json&h=name,ip,node.role,cluster_manager,heap.percent", nil); err == nil {
			_ = json.Unmarshal(b, &s.Nodes)
		}
		if b, _, err := a.do(ctx, http.MethodGet, "/_cat/shards/"+index+"?format=json&h=shard,prirep,state,node,docs,unassigned.reason", nil); err == nil {
			_ = json.Unmarshal(b, &s.Shards)
		}
		if b, _, err := a.do(ctx, http.MethodGet, "/"+index+"/_count", nil); err == nil {
			var c struct {
				Count int `json:"count"`
			}
			_ = json.Unmarshal(b, &c)
			s.DocCnt = c.Count
		}
	}
	sort.Slice(s.Shards, func(i, j int) bool {
		if s.Shards[i].Shard != s.Shards[j].Shard {
			return s.Shards[i].Shard < s.Shards[j].Shard
		}
		return s.Shards[i].Prirep < s.Shards[j].Prirep
	})

	a.mu.Lock()
	defer a.mu.Unlock()
	if s.Status != a.status {
		if a.status != "" {
			a.addEvent("health", fmt.Sprintf("status kluster %s -> %s", a.status, s.Status))
		}
		a.status = s.Status
	}
	if s.Up {
		now := map[string]bool{}
		for _, n := range s.Nodes {
			now[n.Name] = true
			if a.nodes != nil && !a.nodes[n.Name] {
				a.addEvent("node", n.Name+" bergabung ke kluster")
			}
		}
		for n := range a.nodes {
			if !now[n] {
				a.addEvent("node", n+" keluar dari kluster (mati atau terputus)")
			}
		}
		a.nodes = now
		for _, sh := range s.Shards {
			if sh.Prirep != "p" || sh.State != "STARTED" {
				continue
			}
			if prev, ok := a.prim[sh.Shard]; ok && prev != sh.Node {
				a.addEvent("promote", fmt.Sprintf("shard %s: primary pindah dari %s ke %s (replica dipromosikan)", sh.Shard, prev, sh.Node))
			}
			a.prim[sh.Shard] = sh.Node
		}
	}
	a.history = append(a.history, HealthPoint{At: s.At, Status: s.Status})
	if len(a.history) > 180 {
		a.history = a.history[len(a.history)-180:]
	}
	a.last = s
}

func (a *app) loop(ctx context.Context) {
	for {
		if !a.ready {
			if err := a.setup(ctx); err != nil {
				a.log.Warn("menunggu OpenSearch", "err", err.Error())
			} else {
				a.ready = true
			}
		}
		a.observe(ctx)
		time.Sleep(time.Second)
	}
}

func (a *app) handleCluster(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	s := a.last
	s.History = append([]HealthPoint(nil), a.history...)
	s.Events = append([]Event(nil), a.events...)
	a.mu.Unlock()
	httpx.JSON(w, 200, s)
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		q = "kopi"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	b, via, err := a.do(ctx, http.MethodGet, "/"+index+"/_search?size=5&q="+url.QueryEscape("nama:"+q), nil)
	if err != nil {
		httpx.Err(w, 503, err.Error())
		return
	}
	var res struct {
		Took   int             `json:"took"`
		Shards json.RawMessage `json:"_shards"`
		Hits   struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string          `json:"_id"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	_ = json.Unmarshal(b, &res)
	httpx.JSON(w, 200, map[string]any{"via": via, "took_ms": res.Took, "shards": res.Shards, "total": res.Hits.Total.Value, "hits": res.Hits.Hits})
}

func main() {
	log := logx.New("search-api")
	urls := strings.Split(httpx.Env("OS_URLS", "http://os-node1:9200,http://os-node2:9200"), ",")
	a := &app{urls: urls, known: strings.Split(httpx.Env("OS_NODES", "os-node1,os-node2"), ","),
		client: &http.Client{Timeout: 3 * time.Second}, log: log, prim: map[string]string{}}
	go a.loop(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cluster", a.handleCluster)
	mux.HandleFunc("GET /search", a.handleSearch)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	addr := ":" + httpx.Env("PORT", "8080")
	log.Info("mulai", "addr", addr)
	if err := http.ListenAndServe(addr, httpx.Log(log, mux)); err != nil {
		log.Error("berhenti", "err", err.Error())
	}
}
