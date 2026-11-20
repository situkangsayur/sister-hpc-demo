package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/soap"
)

// ujiKunci mengirim beberapa request ke route /publik/* di APISIX yang dijaga
// key-auth dan limit-count, lalu melaporkan status tiap request.
func (k *Klien) ujiKunci(w http.ResponseWriter, r *http.Request) {
	var q struct {
		DenganKunci bool `json:"dengan_kunci"`
		Jumlah      int  `json:"jumlah"`
	}
	if bacaJSON(r, &q) != nil {
		tulisJSON(w, http.StatusBadRequest, map[string]string{"error": "json tidak sah"})
		return
	}
	if q.Jumlah <= 0 || q.Jumlah > 30 {
		q.Jumlah = 8
	}
	type baris struct {
		Ke       int     `json:"ke"`
		Status   int     `json:"status"`
		Sisa     string  `json:"sisa_kuota"`
		Batas    string  `json:"batas"`
		Pesan    string  `json:"pesan"`
		DurasiMs float64 `json:"durasi_ms"`
	}
	var hasil []baris
	for i := 0; i < q.Jumlah; i++ {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, k.gwHTTP+"/publik/v1/produk/42", nil)
		if q.DenganKunci {
			req.Header.Set("apikey", k.apiKey)
		}
		t0 := time.Now()
		resp, err := k.http.Do(req)
		if err != nil {
			hasil = append(hasil, baris{Ke: i + 1, Pesan: err.Error()})
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		hasil = append(hasil, baris{Ke: i + 1, Status: resp.StatusCode, Sisa: resp.Header.Get("X-Ratelimit-Remaining"),
			Batas: resp.Header.Get("X-Ratelimit-Limit"), Pesan: strings.TrimSpace(string(b)), DurasiMs: msSejak(t0)})
	}
	tulisJSON(w, http.StatusOK, map[string]any{"dengan_kunci": q.DenganKunci, "hasil": hasil})
}

// roundRobin memperlihatkan load balancing APISIX ke dua replica katalog.
func (k *Klien) roundRobin(w http.ResponseWriter, r *http.Request) {
	hitung := map[string]int{}
	urutan := []string{}
	for i := 0; i < 12; i++ {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, k.gwHTTP+"/v1/produk/42", nil)
		resp, err := k.http.Do(req)
		if err != nil {
			tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		n := resp.Header.Get("X-Node")
		hitung[n]++
		urutan = append(urutan, n)
	}
	// gRPC: setiap panggilan unary juga diseimbangkan APISIX per request (L7),
	// bukan per koneksi TCP (Subbab Load balancing gRPC).
	grpcUrutan := []string{}
	for i := 0; i < 6; i++ {
		var hdr metadata.MD
		c, b := context.WithTimeout(r.Context(), 2*time.Second)
		_, err := k.gwGRPC.AmbilProduk(c, &pb.AmbilProdukRequest{Id: 42}, grpcHeader(&hdr))
		b()
		if err != nil {
			grpcUrutan = append(grpcUrutan, "error: "+err.Error())
			continue
		}
		grpcUrutan = append(grpcUrutan, strings.Join(hdr.Get("x-node"), ","))
	}
	tulisJSON(w, http.StatusOK, map[string]any{"http": urutan, "hitung": hitung, "grpc": grpcUrutan})
}

// nPlus1 menjalankan query GraphQL yang sama dua kali: tanpa dan dengan DataLoader.
func (k *Klien) nPlus1(w http.ResponseWriter, r *http.Request) {
	query := `{"query":"{ daftarProduk { id nama penjual { nama kota } } }"}`
	satu := func(loader bool) map[string]any {
		var ms []float64
		var sql string
		var ext map[string]any
		var ukuran int
		for i := 0; i < 11; i++ {
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, k.gwHTTP+"/graphql", strings.NewReader(query))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Debug-Extensions", "on")
			if loader {
				req.Header.Set("X-DataLoader", "on")
			}
			t0 := time.Now()
			resp, err := k.http.Do(req)
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if i == 0 {
				continue // pemanasan
			}
			ms = append(ms, msSejak(t0))
			sql = resp.Header.Get("X-Sql-Queries")
			ukuran = len(b)
			var isi struct {
				Extensions map[string]any `json:"extensions"`
			}
			json.Unmarshal(b, &isi)
			ext = isi.Extensions
		}
		n, _ := strconv.Atoi(sql)
		return map[string]any{"dataloader": loader, "sql_queries": n, "latensi": hitungStatistik(ms), "extensions": ext, "bytes": ukuran}
	}
	tulisJSON(w, http.StatusOK, map[string]any{"query": "{ daftarProduk { id nama penjual { nama kota } } }",
		"naif": satu(false), "dataloader": satu(true)})
}

// ubahHarga mengubah harga lewat protokol pilihan; perubahan lalu muncul di
// SSE, long polling, WebSocket-less ticker, dan gRPC stream.
func (k *Klien) ubahHarga(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID       int64  `json:"id"`
		Harga    int64  `json:"harga"`
		Protokol string `json:"protokol"`
	}
	if bacaJSON(r, &q) != nil || q.ID <= 0 {
		tulisJSON(w, http.StatusBadRequest, map[string]string{"error": "json tidak sah"})
		return
	}
	ctx, b := context.WithTimeout(r.Context(), 3*time.Second)
	defer b()
	var req *http.Request
	switch q.Protokol {
	case "grpc":
		p, err := k.gwGRPC.UbahHarga(ctx, &pb.UbahHargaRequest{Id: q.ID, Harga: q.Harga})
		if err != nil {
			tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		tulisJSON(w, http.StatusOK, map[string]any{"protokol": "grpc", "produk": map[string]any{"id": p.Id, "nama": p.Nama, "harga": p.Harga}})
		return
	case "graphql":
		isi := fmt.Sprintf(`{"query":"mutation { ubahHarga(id: %d, harga: %d) { id nama harga } }"}`, q.ID, q.Harga)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, k.gwHTTP+"/graphql", strings.NewReader(isi))
		req.Header.Set("Content-Type", "application/json")
	case "soap":
		isi := soap.AmplopRequest("UbahHarga", fmt.Sprintf("<id>%d</id><harga>%d</harga>", q.ID, q.Harga))
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, k.gwHTTP+"/soap", bytes.NewReader(isi))
		req.Header.Set("Content-Type", "text/xml; charset=utf-8")
		req.Header.Set("SOAPAction", `"`+soap.NS+`#UbahHarga"`)
	default:
		q.Protokol = "rest"
		isi, _ := json.Marshal(map[string]int64{"harga": q.Harga})
		req, _ = http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/v1/produk/%d/harga", k.gwHTTP, q.ID), bytes.NewReader(isi))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.http.Do(req)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	b2, _ := io.ReadAll(resp.Body)
	tulisJSON(w, http.StatusOK, map[string]any{"protokol": q.Protokol, "status": resp.StatusCode, "respons": string(b2)})
}

// ticker meneruskan pengaturan ticker harga ke replica pertama saja (agar tidak ganda).
func (k *Klien) ticker(w http.ResponseWriter, r *http.Request) {
	var body io.Reader
	if r.Method == http.MethodPut {
		b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<12))
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, k.nodes[0]+"/admin/ticker", body)
	resp, err := k.http.Do(req)
	if err != nil {
		tulisJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// pantauGRPC membuka stream gRPC PantauHarga (server streaming) lewat APISIX lalu
// meneruskan setiap pesan ke browser sebagai SSE: gRPC streaming antarbackend,
// SSE ke browser.
func (k *Klien) pantauGRPC(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming tidak didukung", http.StatusInternalServerError)
		return
	}
	ids := []int64{}
	for _, s := range strings.Split(r.URL.Query().Get("id"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		ids = []int64{42, 1, 2, 3}
	}
	st, err := k.gwGRPC.PantauHarga(r.Context(), &pb.PantauHargaRequest{Id: ids})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	hdr, _ := st.Header()
	fmt.Fprintf(w, "event: halo\ndata: {\"node\":%q}\n\n", strings.Join(hdr.Get("x-node"), ","))
	f.Flush()
	for {
		p, err := st.Recv()
		if err != nil {
			return
		}
		b, _ := json.Marshal(map[string]any{"id": p.Id, "nama": p.Nama, "harga": p.Harga, "waktu_ms": time.Now().UnixMilli()})
		fmt.Fprintf(w, "event: harga\ndata: %s\n\n", b)
		f.Flush()
	}
}

// modeGRPC memperagakan client streaming dan bidirectional streaming, dan
// mengembalikan log pesan dengan waktu relatif.
func (k *Klien) modeGRPC(w http.ResponseWriter, r *http.Request) {
	type entri struct {
		Ms    float64 `json:"ms"`
		Arah  string  `json:"arah"` // kirim / terima
		Pesan string  `json:"pesan"`
	}
	ctx, b := context.WithTimeout(r.Context(), 10*time.Second)
	defer b()
	mulai := time.Now()
	catat := func(l *[]entri, arah, pesan string) {
		*l = append(*l, entri{float64(time.Since(mulai).Microseconds()) / 1000, arah, pesan})
	}

	var unary []entri
	catat(&unary, "kirim", "AmbilProduk{id: 42}")
	if p, err := k.gwGRPC.AmbilProduk(ctx, &pb.AmbilProdukRequest{Id: 42}); err == nil {
		catat(&unary, "terima", fmt.Sprintf("Produk{id: %d, nama: %q, harga: %d}", p.Id, p.Nama, p.Harga))
	} else {
		catat(&unary, "terima", "error: "+err.Error())
	}

	mulai = time.Now()
	var server []entri
	sc, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	catat(&server, "kirim", "PantauHarga{id: [42, 1, 2]} (menunggu 1,5 detik)")
	if st, err := k.gwGRPC.PantauHarga(sc, &pb.PantauHargaRequest{Id: []int64{42, 1, 2}}); err == nil {
		for {
			p, err := st.Recv()
			if err != nil {
				catat(&server, "terima", "stream ditutup klien (deadline)")
				break
			}
			catat(&server, "terima", fmt.Sprintf("Produk{id: %d, harga: %d}", p.Id, p.Harga))
		}
	}
	cancel()

	mulai = time.Now()
	var client []entri
	if st, err := k.gwGRPC.CatatPenjualan(ctx); err == nil {
		for i, j := range []int64{2, 1, 5, 3} {
			id := []int64{42, 1, 42, 2}[i]
			st.Send(&pb.Penjualan{ProdukId: id, Jumlah: j})
			catat(&client, "kirim", fmt.Sprintf("Penjualan{produk_id: %d, jumlah: %d}", id, j))
			time.Sleep(60 * time.Millisecond)
		}
		if res, err := st.CloseAndRecv(); err == nil {
			catat(&client, "terima", fmt.Sprintf("Ringkasan{pesan: %d, unit: %d, rupiah: %d}", res.JumlahPesan, res.TotalUnit, res.TotalRupiah))
		} else {
			catat(&client, "terima", "error: "+err.Error())
		}
	}

	mulai = time.Now()
	var bidi []entri
	if st, err := k.gwGRPC.Tawar(ctx); err == nil {
		for _, h := range []int64{15000, 23000, 24000} {
			st.Send(&pb.Tawaran{ProdukId: 42, Harga: h})
			catat(&bidi, "kirim", fmt.Sprintf("Tawaran{produk_id: 42, harga: %d}", h))
			if j, err := st.Recv(); err == nil {
				catat(&bidi, "terima", fmt.Sprintf("Jawaban{diterima: %v, pesan: %q}", j.Diterima, j.Pesan))
			}
			time.Sleep(60 * time.Millisecond)
		}
		st.CloseSend()
	}
	tulisJSON(w, http.StatusOK, map[string]any{"unary": unary, "server_streaming": server, "client_streaming": client, "bidi_streaming": bidi})
}

func grpcHeader(md *metadata.MD) grpc.CallOption { return grpc.Header(md) }
