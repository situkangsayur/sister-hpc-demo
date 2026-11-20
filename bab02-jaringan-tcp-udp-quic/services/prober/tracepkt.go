package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/pcap"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/trace"
)

// TraceParams: rekam satu koneksi lengkap.
type TraceParams struct {
	Proto    string `json:"proto"`    // tcp | udp | quic
	Messages int    `json:"messages"` // 1..5
	Burst    bool   `json:"burst"`    // TCP: dua send() berturut-turut tanpa menunggu balasan
}

var tracePorts = map[string]int{"tcp": 9000, "udp": 9001, "quic": 9002}

func (a *app) handleTrace(w http.ResponseWriter, r *http.Request) {
	var p TraceParams
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, 400, "body tidak valid")
		return
	}
	port, ok := tracePorts[p.Proto]
	if !ok {
		httpx.Error(w, 400, "proto harus tcp|udp|quic")
		return
	}
	p.Messages = max(1, min(p.Messages, 5))
	if !a.busy.TryLock() {
		httpx.Error(w, 409, "eksperimen lain sedang berjalan")
		return
	}
	defer a.busy.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	nm := a.currentNetem(ctx)

	// 1. tcpdump di kedua sisi
	if err := ctlJSON(ctx, "POST", a.cfg.echoCtl+"/capture/start", map[string]string{"proto": p.Proto}, nil); err != nil {
		httpx.Error(w, 502, "capture server: "+err.Error())
		return
	}
	filter := []string{strings.Replace(p.Proto, "quic", "udp", 1), "port", strconv.Itoa(port), "and", "host", a.cfg.echoIP}
	local, err := pcap.Start(a.cfg.iface, filter, 80*time.Second)
	if err != nil {
		_ = ctlJSON(ctx, "POST", a.cfg.echoCtl+"/capture/stop", nil, nil)
		httpx.Error(w, 500, "capture klien: "+err.Error())
		return
	}
	time.Sleep(150 * time.Millisecond)

	// 2. jalankan klien sambil mencatat panggilan socket API
	var ev []trace.Event
	var key string
	var cerr error
	switch p.Proto {
	case "tcp":
		key, ev, cerr = a.traceTCP(p)
	case "udp":
		key, ev, cerr = a.traceUDP(p)
	case "quic":
		key, ev, cerr = a.traceQUIC(ctx, p)
	}
	// tunggu packet penutup (FIN/ACK) yang masih di jalan
	time.Sleep(400*time.Millisecond + 3*time.Duration(nm.DelayMs+nm.JitterMs)*time.Millisecond)

	// 3. hentikan capture, cocokkan dua sisi
	clientRaw := local.Stop()
	var srv struct {
		Raw string `json:"raw"`
	}
	_ = ctlJSON(ctx, "POST", a.cfg.echoCtl+"/capture/stop", nil, &srv)
	var serverEv []trace.Event
	if key != "" {
		_ = ctlJSON(ctx, "GET", a.cfg.echoCtl+"/trace?key="+key, nil, &serverEv)
	}
	ev = append(ev, serverEv...)
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].TS < ev[j].TS })
	cp := pcap.Parse(clientRaw, 9002)
	sp := pcap.Parse(srv.Raw, 9002)
	flows := pcap.Match(cp, sp, port)
	lost, retrans := 0, 0
	for _, f := range flows {
		if f.RecvTS == nil {
			lost++
		}
		if f.Retrans {
			retrans++
		}
	}
	resp := map[string]any{
		"proto": p.Proto, "params": p, "netem": nm, "netem_text": nm.String(),
		"client": key, "server": net.JoinHostPort(a.cfg.echoIP, strconv.Itoa(port)),
		"events": ev, "flows": flows, "lost_packets": lost, "retrans_packets": retrans,
		"client_tcpdump": pcap.Summary(clientRaw), "server_tcpdump": pcap.Summary(srv.Raw),
		"tcpdump_cmd": "tcpdump -i " + a.cfg.iface + " -nn -tt -S " + strings.Join(filter, " "),
	}
	if cerr != nil {
		resp["error"] = cerr.Error()
	}
	a.log.Info("trace selesai", "proto", p.Proto, "packets", len(flows), "lost", lost, "retrans", retrans, "netem", nm.String())
	httpx.JSON(w, 200, resp)
}

func cev(call string, n int, format string, args ...any) trace.Event {
	return trace.Event{TS: trace.Now(), Side: "client", Call: call, Bytes: n, Detail: fmt.Sprintf(format, args...)}
}

func (a *app) traceTCP(p TraceParams) (string, []trace.Event, error) {
	var ev []trace.Event
	ev = append(ev, cev("socket()", 0, "membuat socket TCP"))
	ev = append(ev, cev("connect()", 0, "mulai three-way handshake: kirim SYN"))
	start := time.Now()
	c, err := net.DialTimeout("tcp", a.addr(9000), 15*time.Second)
	if err != nil {
		return "", ev, err
	}
	key := c.LocalAddr().String()
	ev = append(ev, cev("connect() selesai", 0, "SYN-ACK diterima, ACK dikirim (%.1f ms)", ms(time.Since(start))))
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	buf := make([]byte, 4096)
	readLines := func(want int) error {
		got := 0
		for got < want {
			n, err := c.Read(buf)
			if n > 0 {
				lines := strings.Count(string(buf[:n]), "\n")
				got += lines
				ev = append(ev, cev("recv()", n, "recv() = %d byte (%d baris) %q", n, lines, buf[:n]))
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if p.Burst {
		for i := 1; i <= p.Messages; i++ {
			msg := fmt.Sprintf("halo %d\n", i)
			_, _ = c.Write([]byte(msg))
			ev = append(ev, cev("send()", len(msg), "send() %d byte %q tanpa menunggu balasan", len(msg), msg))
		}
		if err := readLines(p.Messages); err != nil {
			return key, ev, err
		}
	} else {
		for i := 1; i <= p.Messages; i++ {
			msg := fmt.Sprintf("halo %d\n", i)
			_, _ = c.Write([]byte(msg))
			ev = append(ev, cev("send()", len(msg), "send() %d byte %q", len(msg), msg))
			if err := readLines(1); err != nil {
				return key, ev, err
			}
		}
	}
	ev = append(ev, cev("close()", 0, "klien menutup socket: kirim FIN"))
	_ = c.Close()
	return key, ev, nil
}

func (a *app) traceUDP(p TraceParams) (string, []trace.Event, error) {
	var ev []trace.Event
	ev = append(ev, cev("socket()", 0, "membuat socket UDP"))
	c, err := net.Dial("udp", a.addr(9001))
	if err != nil {
		return "", ev, err
	}
	key := c.LocalAddr().String()
	ev = append(ev, cev("connect()", 0, "UDP connect() hanya mencatat alamat tujuan; tidak ada packet yang dikirim"))
	buf := make([]byte, 2048)
	for i := 1; i <= p.Messages; i++ {
		// panjang tiap datagram berbeda agar mudah dicocokkan di tangkapan
		msg := fmt.Sprintf("halo %d %s", i, strings.Repeat(".", i))
		_, _ = c.Write([]byte(msg))
		ev = append(ev, cev("sendto()", len(msg), "sendto() %d byte %q", len(msg), msg))
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			ev = append(ev, cev("recvfrom() timeout", 0, "tidak ada balasan dalam 2 detik: datagram (atau balasannya) hilang, UDP tidak mengirim ulang"))
			continue
		}
		ev = append(ev, cev("recvfrom()", n, "recvfrom() = %d byte %q", n, buf[:n]))
	}
	ev = append(ev, cev("close()", 0, "close() pada UDP tidak mengirim packet apa pun"))
	_ = c.Close()
	return key, ev, nil
}

func (a *app) traceQUIC(ctx context.Context, p TraceParams) (string, []trace.Event, error) {
	var ev []trace.Event
	ev = append(ev, cev("DialAddr()", 0, "kirim QUIC Initial (ClientHello TLS 1.3 di dalamnya)"))
	start := time.Now()
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(nil), quicConf)
	if err != nil {
		return "", ev, err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	key := net.JoinHostPort(a.cfg.labIP, strconv.Itoa(port))
	ev = append(ev, cev("handshake selesai", 0, "transport + TLS selesai dalam 1 RTT (%.1f ms)", ms(time.Since(start))))
	st, err := conn.OpenStreamSync(dctx)
	if err != nil {
		return key, ev, err
	}
	ev = append(ev, cev("OpenStreamSync()", 0, "membuka stream %d (tanpa handshake tambahan)", st.StreamID()))
	_ = st.SetDeadline(time.Now().Add(20 * time.Second))
	_, _ = st.Write([]byte{'E'})
	buf := make([]byte, 2048)
	for i := 1; i <= p.Messages; i++ {
		msg := fmt.Sprintf("halo %d\n", i)
		_, _ = st.Write([]byte(msg))
		ev = append(ev, cev("stream.Write()", len(msg), "Write() %d byte %q", len(msg), msg))
		got := 0
		for got < len(msg) {
			n, err := st.Read(buf)
			if n > 0 {
				got += n
				ev = append(ev, cev("stream.Read()", n, "Read() = %d byte %q", n, buf[:n]))
			}
			if err != nil {
				return key, ev, err
			}
		}
	}
	_ = st.Close()
	ev = append(ev, cev("CloseWithError()", 0, "menutup koneksi: kirim CONNECTION_CLOSE"))
	_ = conn.CloseWithError(0, "selesai")
	return key, ev, nil
}
