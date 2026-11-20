package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExpandBindAddr(t *testing.T) {
	t.Setenv("BIND_ADDR", "")
	c := Config{URL: "http://${BIND_ADDR}:50400/api", Target: "${BIND_ADDR}:50411", Headers: map[string]string{"X": "${BIND_ADDR}"}}
	e := c.Expand(nil)
	if e.URL != "http://127.0.0.1:50400/api" || e.Target != "127.0.0.1:50411" || e.Headers["X"] != "127.0.0.1" {
		t.Fatalf("default salah: %+v", e)
	}
	t.Setenv("BIND_ADDR", "10.100.21.22")
	if got := c.Expand(nil).URL; got != "http://10.100.21.22:50400/api" {
		t.Fatalf("env: %s", got)
	}
	if got := c.Expand(map[string]string{"BIND_ADDR": "192.168.1.5"}).URL; got != "http://192.168.1.5:50400/api" {
		t.Fatalf("vars harus menang atas env: %s", got)
	}
	if c.Headers["X"] != "${BIND_ADDR}" {
		t.Fatal("Expand tidak boleh mengubah config asli")
	}
}

func TestValidate(t *testing.T) {
	c := Config{Kind: KindHTTP, URL: "http://x", Method: "post"}
	l, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != "POST" || c.Concurrency != 1 || l.Duration != 10*time.Second || l.Timeout != 5*time.Second {
		t.Fatalf("default salah: %+v %+v", c, l)
	}
	c = Config{Kind: KindHTTP, URL: "http://x", Requests: 50}
	if l, _ := c.Validate(); l.Duration != 0 || l.Requests != 50 {
		t.Fatalf("requests saja: %+v", l)
	}
	bad := []Config{
		{Kind: "ftp"},
		{Kind: KindHTTP},
		{Kind: KindGraphQL, URL: "http://x"},
		{Kind: KindGraphQL, URL: "http://x", Query: "{a}", Variables: "[1]"},
		{Kind: KindGRPC, Target: "x:1"},
		{Kind: KindWS},
		{Kind: KindHTTP, URL: "http://x", Duration: "abc"},
		{Kind: KindHTTP, URL: "http://x", RPS: -1},
	}
	for i, b := range bad {
		if _, err := b.Validate(); err == nil {
			t.Errorf("kasus %d seharusnya error", i)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]error{
		"":                 nil,
		"http_5xx":         &StatusError{Code: 503},
		"http_4xx":         fmt.Errorf("wrap: %w", &StatusError{Code: 404}),
		"http_429":         &StatusError{Code: 429},
		"graphql_errors":   &GraphQLError{Msg: "x"},
		"timeout":          context.DeadlineExceeded,
		"conn_refused":     &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
		"conn_reset":       syscall.ECONNRESET,
		"grpc_Unavailable": status.Error(codes.Unavailable, "x"),
		"connect":          &connectError{err: errors.New("boom")},
		"other":            errors.New("aneh"),
	}
	for want, err := range cases {
		if got := Classify(err); got != want {
			t.Errorf("Classify(%v) = %q, want %q", err, got, want)
		}
	}
	if !strings.HasPrefix(Classify(status.Error(codes.DeadlineExceeded, "x")), "timeout") {
		t.Error("grpc deadline harus timeout")
	}
}
