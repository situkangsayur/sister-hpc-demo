// Service catalog: pemilik data produk (database "catalog") dan pencarian produk lewat
// OpenSearch 2 node (index products: 2 primary shard, 1 replica). Bila OpenSearch tidak
// menjawab, pencarian jatuh ke Postgres (fallback) agar toko tetap bisa dipakai.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"sister-bab05/services/platform"
)

const indexName = "products"

type app struct {
	db      *pgxpool.Pool
	search  *platform.SearchClient
	log     *slog.Logger
	indexed atomic.Bool
}

func main() {
	log := platform.NewLogger("catalog")
	ctx := context.Background()
	db, err := platform.ConnectDB(ctx, platform.Env("DATABASE_URL", "postgres://sister:changeme@postgres:5432/catalog"))
	if err != nil {
		log.Error("db", "err", err)
		return
	}
	if err := platform.Migrate(ctx, db, `CREATE TABLE IF NOT EXISTS products (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL,
		category TEXT NOT NULL, price INT NOT NULL)`); err != nil {
		log.Error("migrate", "err", err)
		return
	}
	for _, p := range platform.SeedProducts {
		_, _ = db.Exec(ctx, `INSERT INTO products VALUES ($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
			p.ID, p.Name, p.Description, p.Category, p.Price)
	}
	a := &app{db: db, log: log, search: platform.NewSearchClient(platform.Env("OPENSEARCH_URLS", "http://opensearch-1:9200,http://opensearch-2:9200"))}
	go a.indexLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		err := db.Ping(r.Context())
		st := http.StatusOK
		if err != nil {
			st = http.StatusServiceUnavailable
		}
		platform.WriteJSON(w, st, map[string]any{"service": "catalog", "db": err == nil, "opensearch_indexed": a.indexed.Load()})
	})
	mux.HandleFunc("GET /search", a.handleSearch)
	mux.HandleFunc("GET /products", a.handleList)
	mux.HandleFunc("GET /products/{id}", a.handleGet)
	log.Info("catalog siap", "addr", ":8080")
	platform.Serve(":8080", platform.Middleware(log, mux))
	select {}
}

// indexLoop membuat index lalu mengisi semua produk lewat _bulk. Diulang sampai berhasil
// karena OpenSearch biasanya siap lebih lambat daripada service Go.
func (a *app) indexLoop(ctx context.Context) {
	for !a.indexed.Load() {
		if err := a.reindex(ctx); err != nil {
			a.log.Warn("menunggu opensearch untuk indexing", "err", err)
			time.Sleep(3 * time.Second)
			continue
		}
		a.indexed.Store(true)
		a.log.Info("index products siap", "docs", len(platform.SeedProducts))
	}
}

func (a *app) reindex(ctx context.Context) error {
	_, _, st, err := a.search.Do(ctx, http.MethodHead, "/"+indexName, nil)
	if err != nil {
		return err
	}
	if st == http.StatusNotFound {
		mapping := `{"settings":{"number_of_shards":2,"number_of_replicas":1},
		 "mappings":{"properties":{"name":{"type":"text"},"description":{"type":"text"},
		 "category":{"type":"keyword"},"price":{"type":"integer"}}}}`
		b, _, st, err := a.search.Do(ctx, http.MethodPut, "/"+indexName, []byte(mapping))
		if err != nil || (st >= 300 && !strings.Contains(string(b), "resource_already_exists")) {
			return fmt.Errorf("buat index: %d %s %v", st, b, err)
		}
	}
	prods, err := a.allProducts(ctx)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, p := range prods {
		fmt.Fprintf(&buf, `{"index":{"_index":%q,"_id":%q}}`+"\n", indexName, p.ID)
		b, _ := json.Marshal(p)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	b, _, st, err := a.search.Do(ctx, http.MethodPost, "/_bulk?refresh=true", buf.Bytes())
	if err != nil || st >= 300 {
		return fmt.Errorf("bulk: %d %s %v", st, b, err)
	}
	return nil
}

func (a *app) allProducts(ctx context.Context) ([]platform.Product, error) {
	rows, err := a.db.Query(ctx, `SELECT id, name, description, category, price FROM products ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []platform.Product
	for rows.Next() {
		var p platform.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.Price); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type hit struct {
	platform.Product
	Score float64 `json:"score"`
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	start := time.Now()
	var query map[string]any
	if q == "" {
		query = map[string]any{"match_all": map[string]any{}}
	} else {
		query = map[string]any{"multi_match": map[string]any{
			"query": q, "fields": []string{"name^3", "description", "category^2"}, "fuzziness": "AUTO"}}
	}
	body, _ := json.Marshal(map[string]any{"size": 20, "query": query})
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var b []byte
	var node string
	var st int
	var err error
	if a.indexed.Load() { // sebelum bulk index selesai, pakai fallback agar hasil tidak kosong
		b, node, st, err = a.search.Do(ctx, http.MethodPost, "/"+indexName+"/_search", body)
	} else {
		err = fmt.Errorf("index products belum siap")
	}
	if err == nil && st == http.StatusOK {
		var res struct {
			Shards struct {
				Total      int `json:"total"`
				Successful int `json:"successful"`
			} `json:"_shards"`
			Hits struct {
				Hits []struct {
					Score  float64          `json:"_score"`
					Source platform.Product `json:"_source"`
				} `json:"hits"`
			} `json:"hits"`
		}
		if json.Unmarshal(b, &res) == nil {
			hits := make([]hit, 0, len(res.Hits.Hits))
			for _, h := range res.Hits.Hits {
				hits = append(hits, hit{h.Source, h.Score})
			}
			platform.WriteJSON(w, http.StatusOK, map[string]any{"source": "opensearch", "node": node,
				"shards": res.Shards, "took_ms": time.Since(start).Milliseconds(), "hits": hits})
			return
		}
	}
	// Fallback: OpenSearch tidak siap atau mati, cari dengan ILIKE di Postgres.
	a.log.Warn("pencarian fallback ke postgres", "request_id", platform.RequestID(r.Context()), "err", err, "status", st)
	rows, err := a.db.Query(r.Context(), `SELECT id, name, description, category, price FROM products
		WHERE $1 = '' OR name ILIKE '%'||$1||'%' OR description ILIKE '%'||$1||'%' OR category ILIKE '%'||$1||'%'
		ORDER BY id LIMIT 20`, q)
	if err != nil {
		platform.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	hits := []hit{}
	for rows.Next() {
		var p platform.Product
		if rows.Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.Price) == nil {
			hits = append(hits, hit{p, 0})
		}
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"source": "postgres-fallback",
		"took_ms": time.Since(start).Milliseconds(), "hits": hits})
}

func (a *app) handleList(w http.ResponseWriter, r *http.Request) {
	p, err := a.allProducts(r.Context())
	if err != nil {
		platform.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	platform.WriteJSON(w, http.StatusOK, p)
}

func (a *app) handleGet(w http.ResponseWriter, r *http.Request) {
	var p platform.Product
	err := a.db.QueryRow(r.Context(), `SELECT id, name, description, category, price FROM products WHERE id=$1`,
		r.PathValue("id")).Scan(&p.ID, &p.Name, &p.Description, &p.Category, &p.Price)
	if err != nil {
		platform.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "produk tidak ditemukan"})
		return
	}
	platform.WriteJSON(w, http.StatusOK, p)
}
