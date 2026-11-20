// Paket gql adalah adapter GraphQL untuk toko. Skemanya:
//
//	type Penjual { id: Int! nama: String! kota: String! }
//	type Produk  { id: Int! nama: String! harga: Int! penjual: Penjual! }
//	type Query    { produk(id: Int!): Produk  daftarProduk: [Produk!]! }
//	type Mutation { ubahHarga(id: Int!, harga: Int!): Produk }
//
// Field penjual sengaja dibuat dalam dua versi untuk memperlihatkan masalah N+1
// (Subbab Jebakan khas: masalah N+1 dan DataLoader). Header X-DataLoader: on
// memakai DataLoader (batching per request); tanpa header itu resolver naif
// menjalankan satu query per produk. Jumlah query SQL dikembalikan di
// extensions.sql_queries.
package gql

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/graphql-go/graphql"

	"sister/bab04/internal/toko"
)

// galat membawa kode mesin di extensions.code.
type galat struct {
	error
	kode string
}

func (g galat) Extensions() map[string]any { return map[string]any{"code": g.kode} }

func keGalat(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, toko.ErrTidakAda):
		return galat{err, "NOT_FOUND"}
	case errors.Is(err, toko.ErrValidasi):
		return galat{err, "BAD_USER_INPUT"}
	}
	slog.ErrorContext(ctx, "graphql: kesalahan internal", "err", err)
	return errors.New("kesalahan internal")
}

// sumber adalah nilai induk untuk resolver field Produk.
type sumber struct {
	toko.Produk
	PenjualID int64
}

// Loader adalah DataLoader mini: kumpulkan semua id penjual dalam satu putaran
// eksekusi, lalu ambil sekaligus dengan WHERE id = ANY(...) (Algoritma b04-dataloader).
type Loader struct {
	toko  *toko.Toko
	mu    sync.Mutex
	antre map[int64]bool
	cache map[int64]toko.Penjual
	err   error
}

// Muat mendaftarkan id dan mengembalikan thunk; thunk pertama yang dipanggil
// menjalankan satu query untuk semua id yang sudah terkumpul.
func (l *Loader) Muat(ctx context.Context, id int64) func() (any, error) {
	l.mu.Lock()
	if _, ada := l.cache[id]; !ada {
		l.antre[id] = true
	}
	l.mu.Unlock()
	return func() (any, error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if len(l.antre) > 0 {
			ids := make([]int64, 0, len(l.antre))
			for k := range l.antre {
				ids = append(ids, k)
			}
			l.antre = map[int64]bool{}
			hasil, err := l.toko.PenjualBanyak(ctx, ids)
			if err != nil {
				l.err = err
			}
			for k, v := range hasil {
				l.cache[k] = v
			}
		}
		if l.err != nil {
			return nil, l.err
		}
		return l.cache[id], nil
	}
}

type kunciLoader struct{}

// Skema membangun skema GraphQL.
func Skema(t *toko.Toko) (graphql.Schema, error) {
	wajib := func(x graphql.Type) *graphql.NonNull { return graphql.NewNonNull(x) }
	tipePenjual := graphql.NewObject(graphql.ObjectConfig{Name: "Penjual", Fields: graphql.Fields{
		"id":   {Type: wajib(graphql.Int), Resolve: func(p graphql.ResolveParams) (any, error) { return int(p.Source.(toko.Penjual).ID), nil }},
		"nama": {Type: wajib(graphql.String), Resolve: func(p graphql.ResolveParams) (any, error) { return p.Source.(toko.Penjual).Nama, nil }},
		"kota": {Type: wajib(graphql.String), Resolve: func(p graphql.ResolveParams) (any, error) { return p.Source.(toko.Penjual).Kota, nil }},
	}})
	tipeProduk := graphql.NewObject(graphql.ObjectConfig{Name: "Produk", Fields: graphql.Fields{
		"id":    {Type: wajib(graphql.Int), Resolve: func(p graphql.ResolveParams) (any, error) { return int(p.Source.(sumber).ID), nil }},
		"nama":  {Type: wajib(graphql.String), Resolve: func(p graphql.ResolveParams) (any, error) { return p.Source.(sumber).Nama, nil }},
		"harga": {Type: wajib(graphql.Int), Resolve: func(p graphql.ResolveParams) (any, error) { return int(p.Source.(sumber).Harga), nil }},
		"penjual": {Type: wajib(tipePenjual), Resolve: func(p graphql.ResolveParams) (any, error) {
			s := p.Source.(sumber)
			if l, ok := p.Context.Value(kunciLoader{}).(*Loader); ok {
				return l.Muat(p.Context, s.PenjualID), nil // DataLoader: ditunda, lalu di-batch
			}
			pj, err := t.Penjual(p.Context, s.PenjualID) // naif: satu query per produk
			if err != nil {
				return nil, keGalat(p.Context, err)
			}
			return pj, nil
		}},
	}})
	query := graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: graphql.Fields{
		"produk": {Type: tipeProduk, Args: graphql.FieldConfigArgument{"id": {Type: wajib(graphql.Int)}},
			Resolve: func(p graphql.ResolveParams) (any, error) {
				pr, pj, err := t.AmbilLengkap(p.Context, int64(p.Args["id"].(int)))
				if err != nil {
					return nil, keGalat(p.Context, err)
				}
				return sumber{pr, pj}, nil
			}},
		"daftarProduk": {Type: wajib(graphql.NewList(wajib(tipeProduk))),
			Resolve: func(p graphql.ResolveParams) (any, error) {
				d, pj, err := t.ProdukDenganPenjual(p.Context)
				if err != nil {
					return nil, keGalat(p.Context, err)
				}
				hasil := make([]sumber, len(d))
				for i, x := range d {
					hasil[i] = sumber{x, pj[x.ID]}
				}
				return hasil, nil
			}},
	}})
	mutation := graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: graphql.Fields{
		"ubahHarga": {Type: tipeProduk, Args: graphql.FieldConfigArgument{
			"id": {Type: wajib(graphql.Int)}, "harga": {Type: wajib(graphql.Int)},
		}, Resolve: func(p graphql.ResolveParams) (any, error) {
			pr, err := t.UbahHarga(toko.DenganOleh(p.Context, "graphql"), int64(p.Args["id"].(int)), int64(p.Args["harga"].(int)))
			if err != nil {
				return nil, keGalat(p.Context, err)
			}
			_, pj, err := t.AmbilLengkap(p.Context, pr.ID)
			if err != nil {
				return nil, keGalat(p.Context, err)
			}
			return sumber{pr, pj}, nil
		}},
	}})
	return graphql.NewSchema(graphql.SchemaConfig{Query: query, Mutation: mutation})
}

// Handler melayani POST /graphql. Error bisnis tetap HTTP 200 dengan larik errors,
// sesuai kebiasaan GraphQL lewat HTTP.
func Handler(skema graphql.Schema, t *toko.Toko, node string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&m); err != nil {
			http.Error(w, "isi request bukan JSON yang sah", http.StatusBadRequest)
			return
		}
		ctx, n := toko.DenganPenghitung(r.Context())
		pakaiLoader := r.Header.Get("X-DataLoader") == "on"
		if pakaiLoader {
			ctx = context.WithValue(ctx, kunciLoader{}, &Loader{toko: t, antre: map[int64]bool{}, cache: map[int64]toko.Penjual{}})
		}
		mulai := time.Now()
		hasil := graphql.Do(graphql.Params{
			Schema: skema, RequestString: m.Query, VariableValues: m.Variables,
			OperationName: m.OperationName, Context: ctx,
		})
		durasi := time.Since(mulai)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-SQL-Queries", itoa(n.Load()))
		w.Header().Set("Access-Control-Expose-Headers", "X-SQL-Queries")
		// extensions hanya dikirim bila diminta, agar ukuran byte playground adil
		if r.Header.Get("X-Debug-Extensions") == "on" {
			hasil.Extensions = map[string]any{"sql_queries": n.Load(), "dataloader": pakaiLoader,
				"durasi_ms": float64(durasi.Microseconds()) / 1000, "node": node}
		}
		slog.InfoContext(r.Context(), "graphql", "operation", m.OperationName, "sql_queries", n.Load(),
			"dataloader", pakaiLoader, "durasi_ms", float64(durasi.Microseconds())/1000, "request_id", r.Header.Get("X-Request-Id"))
		b, _ := json.Marshal(hasil)
		w.Write(b)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
