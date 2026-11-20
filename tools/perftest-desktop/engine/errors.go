package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/coder/websocket"
	"google.golang.org/grpc/status"
)

// StatusError adalah respons HTTP dengan status >= 400.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// GraphQLError berarti respons GraphQL berisi field "errors" yang tidak kosong.
type GraphQLError struct{ Msg string }

func (e *GraphQLError) Error() string { return "graphql: " + e.Msg }

// Classify memetakan error menjadi jenis singkat untuk tabel "error per jenis":
// timeout, conn_refused, conn_reset, dns, tls, http_4xx, http_5xx, http_<kode>, graphql_errors,
// grpc_<Code>, ws_closed, connect, other.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	var se *StatusError
	if errors.As(err, &se) {
		switch {
		case se.Code == 429:
			return "http_429"
		case se.Code >= 500:
			return "http_5xx"
		case se.Code >= 400:
			return "http_4xx"
		}
		return fmt.Sprintf("http_%d", se.Code)
	}
	var ge *GraphQLError
	if errors.As(err, &ge) {
		return "graphql_errors"
	}
	if st, ok := status.FromError(err); ok && st.Code() != 0 {
		if st.Code().String() == "DeadlineExceeded" {
			return "timeout"
		}
		return "grpc_" + st.Code().String()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "conn_refused"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "conn_reset"
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return "dns"
	}
	if websocket.CloseStatus(err) != -1 {
		return "ws_closed"
	}
	var ce *connectError
	if errors.As(err, &ce) {
		return "connect"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:"):
		return "tls"
	case strings.Contains(msg, "connection refused"):
		return "conn_refused"
	case strings.Contains(msg, "EOF"):
		return "conn_reset"
	}
	return "other"
}

// connectError membungkus kegagalan membuka koneksi (mis. dial WebSocket).
type connectError struct{ err error }

func (e *connectError) Error() string { return "connect: " + e.err.Error() }
func (e *connectError) Unwrap() error { return e.err }
