package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/trace"
)

// serveQUIC: satu koneksi QUIC membawa banyak stream yang urutannya independen.
func (s *server) serveQUIC(addr string, base *tls.Config) {
	conf := base.Clone()
	conf.NextProtos = []string{ALPN}
	ln, err := quic.ListenAddrEarly(addr, conf, &quic.Config{
		Allow0RTT:          true, // izinkan data 0-RTT pada koneksi ulang (hanya aman untuk request idempotent)
		MaxIdleTimeout:     30 * time.Second,
		MaxIncomingStreams: 200,
	})
	if err != nil {
		s.log.Error("listen quic", "err", err)
		os.Exit(1)
	}
	for {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			continue
		}
		go s.handleQUICConn(conn)
	}
}

func (s *server) handleQUICConn(conn *quic.Conn) {
	key := conn.RemoteAddr().String()
	s.ring.Add(key, trace.Event{Side: "server", Call: "Accept()", Detail: "koneksi QUIC baru dari " + key})
	for {
		st, err := conn.AcceptStream(context.Background())
		if err != nil {
			s.ring.Add(key, trace.Event{Side: "server", Call: "close", Detail: "koneksi QUIC ditutup: " + err.Error()})
			return
		}
		s.ring.Add(key, trace.Event{Side: "server", Call: "AcceptStream()", Detail: fmt.Sprintf("stream %d dibuka", st.StreamID())})
		go s.handleQUICStream(key, st)
	}
}

func (s *server) handleQUICStream(key string, st *quic.Stream) {
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(2 * time.Minute))
	mode := make([]byte, 1)
	if _, err := io.ReadFull(st, mode); err != nil {
		return
	}
	switch mode[0] {
	case 'S': // sink: hitung sampai klien menutup arah kirimnya
		n, _ := io.Copy(io.Discard, st)
		fmt.Fprintf(st, "%d\n", n)
	default: // 'E': echo
		buf := make([]byte, 16<<10)
		for {
			n, err := st.Read(buf)
			if n > 0 {
				s.ring.Add(key, trace.Event{Side: "server", Call: "stream.Read()", Bytes: n,
					Detail: fmt.Sprintf("stream %d: Read() = %d byte %s", st.StreamID(), n, preview(buf[:n]))})
				if _, werr := st.Write(buf[:n]); werr != nil {
					return
				}
				s.ring.Add(key, trace.Event{Side: "server", Call: "stream.Write()", Bytes: n,
					Detail: fmt.Sprintf("stream %d: Write() %d byte", st.StreamID(), n)})
			}
			if err != nil {
				return
			}
		}
	}
}
