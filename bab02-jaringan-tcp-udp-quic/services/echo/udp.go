package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/trace"
)

type sinkStat struct {
	count, bytes int
	first, last  time.Time
}

// udpSinks menghitung datagram per id uji throughput.
type udpSinks struct {
	mu    sync.Mutex
	order []string
	m     map[string]*sinkStat
}

func newUDPSinks() *udpSinks { return &udpSinks{m: map[string]*sinkStat{}} }

func (u *udpSinks) add(id string, n int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	st, ok := u.m[id]
	if !ok {
		st = &sinkStat{first: time.Now()}
		u.m[id] = st
		u.order = append(u.order, id)
		if len(u.order) > 50 {
			delete(u.m, u.order[0])
			u.order = u.order[1:]
		}
	}
	st.count++
	st.bytes += n
	st.last = time.Now()
}

func (u *udpSinks) stat(id string) sinkStat {
	u.mu.Lock()
	defer u.mu.Unlock()
	if st, ok := u.m[id]; ok {
		return *st
	}
	return sinkStat{}
}

// serveUDP: tidak ada accept(), tidak ada koneksi. Setiap datagram berdiri sendiri.
func (s *server) serveUDP(addr string) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		s.log.Error("listen udp", "err", err)
		os.Exit(1)
	}
	if uc, ok := pc.(*net.UDPConn); ok {
		_ = uc.SetReadBuffer(4 << 20)
	}
	buf := make([]byte, 65535)
	for {
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		msg := buf[:n]
		switch {
		case bytes.HasPrefix(msg, []byte("SINK:")):
			if id, ok := field(msg, 1); ok {
				s.sinks.add(id, n)
			}
		case bytes.HasPrefix(msg, []byte("STAT:")):
			id, _ := field(msg, 1)
			st := s.sinks.stat(id)
			var f, l int64
			if st.count > 0 {
				f, l = st.first.UnixMicro(), st.last.UnixMicro()
			}
			_, _ = pc.WriteTo([]byte(fmt.Sprintf("STAT:%s:%d:%d:%d:%d", id, st.count, st.bytes, f, l)), raddr)
		default:
			key := raddr.String()
			s.ring.Add(key, trace.Event{Side: "server", Call: "recvfrom()", Bytes: n,
				Detail: fmt.Sprintf("recvfrom() = %d byte dari %s %s", n, key, preview(msg))})
			if _, err := pc.WriteTo(msg, raddr); err == nil {
				s.ring.Add(key, trace.Event{Side: "server", Call: "sendto()", Bytes: n,
					Detail: fmt.Sprintf("sendto() %d byte ke %s", n, key)})
			}
		}
	}
}

// field mengambil bagian ke-i dari pesan "A:B:C".
func field(msg []byte, i int) (string, bool) {
	parts := bytes.SplitN(msg, []byte(":"), i+2)
	if len(parts) <= i {
		return "", false
	}
	return string(parts[i]), true
}
