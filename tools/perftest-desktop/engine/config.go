// Package engine berisi mesin perftest: load generator (worker, pacing RPS, batas durasi atau
// jumlah request), klien per protokol (HTTP, GraphQL, gRPC, WebSocket), dan statistik latency.
// Package ini tidak bergantung pada UI sehingga dipakai bersama oleh aplikasi desktop dan CLI.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Kind adalah jenis target.
type Kind string

// Jenis target yang didukung.
const (
	KindHTTP    Kind = "http"
	KindGraphQL Kind = "graphql"
	KindGRPC    Kind = "grpc"
	KindWS      Kind = "ws"
)

// Config adalah satu skenario perftest. Field yang tidak relevan untuk Kind tertentu diabaikan.
// Durasi ditulis sebagai teks Go ("10s", "1m30s") supaya mudah ditulis di YAML dan form UI.
type Config struct {
	Name string `json:"name" yaml:"name"`
	Kind Kind   `json:"kind" yaml:"kind"`

	// HTTP, GraphQL, WebSocket
	URL     string            `json:"url,omitempty" yaml:"url,omitempty"`
	Method  string            `json:"method,omitempty" yaml:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body    string            `json:"body,omitempty" yaml:"body,omitempty"`

	// GraphQL
	Query     string `json:"query,omitempty" yaml:"query,omitempty"`
	Variables string `json:"variables,omitempty" yaml:"variables,omitempty"` // teks JSON objek

	// gRPC: Target "host:port", Method "paket.Service/Method", Request berupa JSON.
	Target     string `json:"target,omitempty" yaml:"target,omitempty"`
	GRPCMethod string `json:"grpcMethod,omitempty" yaml:"grpcMethod,omitempty"`
	Request    string `json:"request,omitempty" yaml:"request,omitempty"`
	TLS        bool   `json:"tls,omitempty" yaml:"tls,omitempty"` // gRPC dengan TLS (default plaintext)

	// WebSocket: Message dikirim tiap iterasi; bila NoReply false, tunggu satu pesan balasan.
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
	NoReply bool   `json:"noReply,omitempty" yaml:"noReply,omitempty"`

	// Beban
	Concurrency int     `json:"concurrency" yaml:"concurrency"`
	Duration    string  `json:"duration,omitempty" yaml:"duration,omitempty"` // mis. "10s"; kosong = pakai Requests
	Requests    int     `json:"requests,omitempty" yaml:"requests,omitempty"` // total request; 0 = tak terbatas (pakai Duration)
	RPS         float64 `json:"rps,omitempty" yaml:"rps,omitempty"`           // target RPS total; 0 = secepat mungkin
	Timeout     string  `json:"timeout,omitempty" yaml:"timeout,omitempty"`   // per request, default "5s"

	// InsecureSkipVerify menerima sertifikat self-signed (demo TLS Bab 3). Hanya untuk lab.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
}

// DefaultBindAddr dipakai bila variabel lingkungan BIND_ADDR kosong.
const DefaultBindAddr = "127.0.0.1"

// Expand mengganti ${BIND_ADDR} (dan ${VAR} lain dari vars atau environment) di semua field teks.
// vars boleh nil. BIND_ADDR default 127.0.0.1.
func (c Config) Expand(vars map[string]string) Config {
	look := func(k string) string {
		if v, ok := vars[k]; ok && v != "" {
			return v
		}
		if v := os.Getenv(k); v != "" {
			return v
		}
		if k == "BIND_ADDR" {
			return DefaultBindAddr
		}
		return "${" + k + "}"
	}
	ex := func(s string) string {
		if !strings.Contains(s, "$") {
			return s
		}
		return os.Expand(s, look)
	}
	out := c
	out.URL = ex(c.URL)
	out.Target = ex(c.Target)
	out.Body = ex(c.Body)
	out.Message = ex(c.Message)
	if c.Headers != nil {
		out.Headers = make(map[string]string, len(c.Headers))
		for k, v := range c.Headers {
			out.Headers[k] = ex(v)
		}
	}
	return out
}

// Limits adalah hasil parsing batas beban.
type Limits struct {
	Duration time.Duration
	Requests int
	Timeout  time.Duration
}

// Validate memeriksa Config dan mengembalikan batas beban yang sudah diparse.
// Bila Duration dan Requests sama-sama kosong, durasi default 10 detik.
func (c *Config) Validate() (Limits, error) {
	var l Limits
	var errs []error
	switch c.Kind {
	case KindHTTP:
		if c.URL == "" {
			errs = append(errs, errors.New("url wajib untuk http"))
		}
		if c.Method == "" {
			c.Method = "GET"
		}
		c.Method = strings.ToUpper(c.Method)
	case KindGraphQL:
		if c.URL == "" || c.Query == "" {
			errs = append(errs, errors.New("url dan query wajib untuk graphql"))
		}
		if strings.TrimSpace(c.Variables) != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(c.Variables), &m); err != nil {
				errs = append(errs, fmt.Errorf("variables bukan objek JSON: %w", err))
			}
		}
	case KindGRPC:
		if c.Target == "" || c.GRPCMethod == "" {
			errs = append(errs, errors.New("target dan grpcMethod wajib untuk grpc"))
		}
	case KindWS:
		if c.URL == "" {
			errs = append(errs, errors.New("url wajib untuk ws"))
		}
	default:
		errs = append(errs, fmt.Errorf("kind tidak dikenal %q (http|graphql|grpc|ws)", c.Kind))
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.Concurrency > 5000 {
		errs = append(errs, errors.New("concurrency maksimal 5000"))
	}
	if c.RPS < 0 {
		errs = append(errs, errors.New("rps tidak boleh negatif"))
	}
	if c.Requests < 0 {
		errs = append(errs, errors.New("requests tidak boleh negatif"))
	}
	l.Requests = c.Requests
	if c.Duration != "" {
		d, err := time.ParseDuration(c.Duration)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("duration tidak valid %q", c.Duration))
		}
		l.Duration = d
	}
	if l.Duration == 0 && l.Requests == 0 {
		l.Duration = 10 * time.Second
	}
	l.Timeout = 5 * time.Second
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("timeout tidak valid %q", c.Timeout))
		}
		l.Timeout = d
	}
	return l, errors.Join(errs...)
}
