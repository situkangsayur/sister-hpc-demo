package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Target adalah sumber daya bersama satu run (mis. http.Client, koneksi gRPC).
// Setiap goroutine worker meminta Worker sendiri lewat NewWorker.
type Target interface {
	NewWorker() Worker
	Close() error
}

// Worker menjalankan satu request per panggilan Do. Dipakai oleh satu goroutine saja.
type Worker interface {
	Do(ctx context.Context) error
	Close() error
}

// NewTarget membuat Target sesuai cfg.Kind. cfg harus sudah lolos Validate.
func NewTarget(ctx context.Context, cfg Config) (Target, error) {
	switch cfg.Kind {
	case KindHTTP, KindGraphQL:
		return newHTTPTarget(cfg)
	case KindGRPC:
		return newGRPCTarget(ctx, cfg)
	case KindWS:
		return &wsTarget{cfg: cfg}, nil
	}
	return nil, fmt.Errorf("kind tidak dikenal %q", cfg.Kind)
}

// ---------- HTTP dan GraphQL ----------

type httpTarget struct {
	cfg    Config
	client *http.Client
	body   []byte
	method string
	gql    bool
}

func newHTTPTarget(cfg Config) (*httpTarget, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = cfg.Concurrency * 2
	tr.MaxIdleConnsPerHost = cfg.Concurrency * 2
	tr.MaxConnsPerHost = 0
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // demo lab, opsi eksplisit
	}
	t := &httpTarget{cfg: cfg, client: &http.Client{Transport: tr}, method: cfg.Method}
	if cfg.Kind == KindGraphQL {
		t.gql = true
		t.method = http.MethodPost
		payload := map[string]any{"query": cfg.Query}
		if v := strings.TrimSpace(cfg.Variables); v != "" {
			payload["variables"] = json.RawMessage(v)
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		t.body = b
	} else if cfg.Body != "" {
		t.body = []byte(cfg.Body)
	}
	if _, err := http.NewRequest(t.method, cfg.URL, nil); err != nil {
		return nil, fmt.Errorf("url tidak valid: %w", err)
	}
	return t, nil
}

func (t *httpTarget) NewWorker() Worker { return t }
func (t *httpTarget) Close() error {
	t.client.CloseIdleConnections()
	return nil
}

func (t *httpTarget) Do(ctx context.Context) error {
	var body io.Reader
	if t.body != nil {
		body = bytes.NewReader(t.body)
	}
	req, err := http.NewRequestWithContext(ctx, t.method, t.cfg.URL, body)
	if err != nil {
		return err
	}
	if t.gql || (t.body != nil && t.cfg.Headers["Content-Type"] == "" && t.cfg.Headers["content-type"] == "") {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if t.gql && resp.StatusCode < 400 {
		var out struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
			return fmt.Errorf("respons graphql bukan JSON: %w", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if len(out.Errors) > 0 {
			return &GraphQLError{Msg: out.Errors[0].Message}
		}
		return nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return &StatusError{Code: resp.StatusCode}
	}
	return nil
}

// ---------- WebSocket ----------

type wsTarget struct{ cfg Config }

func (t *wsTarget) NewWorker() Worker { return &wsWorker{cfg: t.cfg} }
func (t *wsTarget) Close() error      { return nil }

// wsWorker memegang satu koneksi WebSocket; bila putus, dial ulang pada Do berikutnya.
type wsWorker struct {
	cfg  Config
	conn *websocket.Conn
}

func (w *wsWorker) dial(ctx context.Context) error {
	opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
	for k, v := range w.cfg.Headers {
		opts.HTTPHeader.Set(k, v)
	}
	if w.cfg.InsecureSkipVerify {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // demo lab
		opts.HTTPClient = &http.Client{Transport: tr}
	}
	c, resp, err := websocket.Dial(ctx, w.cfg.URL, opts)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return &connectError{err: err}
	}
	c.SetReadLimit(1 << 20)
	w.conn = c
	return nil
}

func (w *wsWorker) Do(ctx context.Context) error {
	if w.conn == nil {
		if err := w.dial(ctx); err != nil {
			// jeda kecil supaya server mati tidak membuat loop dial berputar tanpa henti
			t := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
			case <-t.C:
			}
			t.Stop()
			return err
		}
	}
	msg := w.cfg.Message
	if msg == "" {
		msg = "ping"
	}
	if err := w.conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		w.reset()
		return err
	}
	if w.cfg.NoReply {
		return nil
	}
	if _, _, err := w.conn.Read(ctx); err != nil {
		w.reset()
		return err
	}
	return nil
}

func (w *wsWorker) reset() {
	if w.conn != nil {
		_ = w.conn.CloseNow()
		w.conn = nil
	}
}

func (w *wsWorker) Close() error {
	if w.conn != nil {
		err := w.conn.Close(websocket.StatusNormalClosure, "selesai")
		w.conn = nil
		return err
	}
	return nil
}
