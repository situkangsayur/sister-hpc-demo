package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func TestRunHTTPRequestCount(t *testing.T) {
	var hits atomic.Int64
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		gotBody.Store(string(b) + "|" + r.Method + "|" + r.Header.Get("X-Demo"))
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	var ticks atomic.Int64
	rep, err := Run(context.Background(), Config{
		Kind: KindHTTP, URL: srv.URL, Method: "POST", Body: `{"a":1}`,
		Headers: map[string]string{"X-Demo": "ya"}, Concurrency: 8, Requests: 200,
	}, Options{TickInterval: 50 * time.Millisecond, OnTick: func(Point) { ticks.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 200 || rep.Total != 200 || rep.OK != 200 || rep.Errors != 0 {
		t.Fatalf("hits=%d report=%+v", hits.Load(), rep)
	}
	if gotBody.Load() != `{"a":1}|POST|ya` {
		t.Errorf("body/method/header tidak terkirim: %v", gotBody.Load())
	}
	if rep.Latency.P50 <= 0 || rep.Latency.P99 < rep.Latency.P50 || len(rep.Histogram) == 0 {
		t.Errorf("statistik latency kosong: %+v", rep.Latency)
	}
	if ticks.Load() == 0 {
		t.Error("OnTick tidak pernah dipanggil")
	}
}

func TestRunHTTPErrorsByKind(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) % 4 {
		case 0:
			w.WriteHeader(500)
		case 1:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	rep, err := Run(context.Background(), Config{Kind: KindHTTP, URL: srv.URL, Concurrency: 4, Requests: 400}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ErrorsByKind["http_5xx"] != 100 || rep.ErrorsByKind["http_4xx"] != 100 || rep.OK != 200 {
		t.Fatalf("errors per jenis salah: %+v ok=%d", rep.ErrorsByKind, rep.OK)
	}
}

func TestRunConnRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	rep, err := Run(context.Background(), Config{Kind: KindHTTP, URL: "http://" + addr, Concurrency: 2, Requests: 10}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ErrorsByKind["conn_refused"] != 10 {
		t.Fatalf("harus conn_refused: %+v", rep.ErrorsByKind)
	}
}

func TestRunTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	rep, err := Run(context.Background(), Config{Kind: KindHTTP, URL: srv.URL, Concurrency: 2, Requests: 4, Timeout: "50ms"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ErrorsByKind["timeout"] != 4 {
		t.Fatalf("harus timeout: %+v", rep.ErrorsByKind)
	}
}

func TestRunRPSAndDuration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rep, err := Run(context.Background(), Config{Kind: KindHTTP, URL: srv.URL, Concurrency: 10, RPS: 200, Duration: "1s"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total < 150 || rep.Total > 230 {
		t.Fatalf("RPS 200 selama 1 detik menghasilkan %d request", rep.Total)
	}
	if rep.ElapsedS < 0.9 || rep.ElapsedS > 1.5 {
		t.Fatalf("durasi %v", rep.ElapsedS)
	}
}

func TestRunStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	rep, err := Run(ctx, Config{Kind: KindHTTP, URL: srv.URL, Concurrency: 2, Duration: "1m"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Stopped || rep.ElapsedS > 2 || rep.Errors != 0 {
		t.Fatalf("stop salah: stopped=%v elapsed=%v errors=%+v", rep.Stopped, rep.ElapsedS, rep.ErrorsByKind)
	}
}

func TestRunGraphQL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(in.Query, "salah") || in.Variables["id"] != "7" {
			_, _ = w.Write([]byte(`{"errors":[{"message":"field salah"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"buku":{"id":"7"}}}`))
	}))
	defer srv.Close()
	ok, err := Run(context.Background(), Config{Kind: KindGraphQL, URL: srv.URL, Query: "query($id:ID!){buku(id:$id){id}}", Variables: `{"id":"7"}`, Requests: 20, Concurrency: 2}, Options{})
	if err != nil || ok.OK != 20 {
		t.Fatalf("graphql ok: %v %+v", err, ok.ErrorsByKind)
	}
	bad, err := Run(context.Background(), Config{Kind: KindGraphQL, URL: srv.URL, Query: "{salah}", Requests: 5}, Options{})
	if err != nil || bad.ErrorsByKind["graphql_errors"] != 5 {
		t.Fatalf("graphql errors: %v %+v", err, bad.ErrorsByKind)
	}
}

func TestRunWebSocket(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conns.Add(1)
		defer c.CloseNow()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, bytes.ToUpper(b)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	rep, err := Run(context.Background(), Config{Kind: KindWS, URL: url, Message: "halo", Concurrency: 4, Requests: 100}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK != 100 || conns.Load() != 4 {
		t.Fatalf("ws: ok=%d conns=%d err=%+v", rep.OK, conns.Load(), rep.ErrorsByKind)
	}
	// server tidak ada: dial gagal, dihitung conn_refused
	rep, _ = Run(context.Background(), Config{Kind: KindWS, URL: "ws://127.0.0.1:1/x", Requests: 3}, Options{})
	if rep.ErrorsByKind["conn_refused"] != 3 {
		t.Fatalf("ws mati harus connect: %+v", rep.ErrorsByKind)
	}
}

func startGRPC(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, hs)
	reflection.Register(s)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(s.Stop)
	return l.Addr().String()
}

func TestGRPCReflectionAndRun(t *testing.T) {
	addr := startGRPC(t)
	ms, err := ListGRPCMethods(context.Background(), addr, false, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ms {
		if m.Method == "grpc.health.v1.Health/Check" {
			found = true
			if !strings.Contains(m.Example, "service") {
				t.Errorf("contoh request tidak memuat field: %s", m.Example)
			}
		}
		if strings.HasSuffix(m.Method, "/Watch") {
			t.Error("method streaming tidak boleh didaftar")
		}
	}
	if !found {
		t.Fatalf("Health/Check tidak ditemukan: %+v", ms)
	}
	rep, err := Run(context.Background(), Config{Kind: KindGRPC, Target: addr, GRPCMethod: "grpc.health.v1.Health/Check", Request: `{"service":""}`, Concurrency: 4, Requests: 100}, Options{})
	if err != nil || rep.OK != 100 {
		t.Fatalf("grpc run: %v %+v", err, rep.ErrorsByKind)
	}
	// service yang tidak terdaftar di health server -> NotFound
	rep, err = Run(context.Background(), Config{Kind: KindGRPC, Target: addr, GRPCMethod: "/grpc.health.v1.Health/Check", Request: `{"service":"tidak-ada"}`, Requests: 5}, Options{})
	if err != nil || rep.ErrorsByKind["grpc_NotFound"] != 5 {
		t.Fatalf("grpc NotFound: %v %+v", err, rep.ErrorsByKind)
	}
	if _, err := Run(context.Background(), Config{Kind: KindGRPC, Target: addr, GRPCMethod: "x.Nope/Do", Requests: 1}, Options{}); err == nil {
		t.Fatal("method tak dikenal harus error")
	}
	if _, err := Run(context.Background(), Config{Kind: KindGRPC, Target: addr, GRPCMethod: "grpc.health.v1.Health/Check", Request: `{"salah":1}`, Requests: 1}, Options{}); err == nil {
		t.Fatal("request JSON salah harus error")
	}
}

func TestExportCSVJSON(t *testing.T) {
	r := NewRecorder(time.Unix(0, 0))
	r.Add(Sample{Latency: time.Millisecond})
	r.Add(Sample{Err: "timeout"})
	r.Tick(time.Unix(1, 0))
	rep := r.Report(Config{Name: "uji", Kind: KindHTTP}, time.Unix(1, 0))
	var b bytes.Buffer
	if err := rep.WriteCSV(&b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "t_s,rps") || !strings.HasPrefix(lines[2], "total,") || !strings.HasSuffix(lines[2], "timeout=1") {
		t.Fatalf("csv:\n%s", b.String())
	}
	b.Reset()
	if err := rep.WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back.Total != 2 || back.Config.Name != "uji" {
		t.Fatalf("json: %v %+v", err, back)
	}
}
