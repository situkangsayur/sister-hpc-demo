package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

func ms(d time.Duration) float64 { return round(float64(d.Microseconds()) / 1000) }

func (a *app) addr(port int) string { return net.JoinHostPort(a.cfg.echoIP, strconv.Itoa(port)) }

// tlsClient: sertifikat echo self-signed dibuat saat runtime, jadi verifikasi
// dimatikan. Ini hanya untuk demo; aplikasi sungguhan wajib memverifikasi.
func tlsClient(cache tls.ClientSessionCache) *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{ALPN}, MinVersion: tls.VersionTLS13,
		ClientSessionCache: cache} //nolint:gosec // demo kelas
}

var quicConf = &quic.Config{HandshakeIdleTimeout: 15 * time.Second, MaxIdleTimeout: 30 * time.Second}

func padMsg(prefix string, size int) string {
	if n := size - len(prefix) - 1; n > 0 {
		return prefix + strings.Repeat("x", n) + "\n"
	}
	return prefix + "\n"
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// --- RTT per message -----------------------------------------------------

// tcpLatency: satu koneksi TCP, kirim message lalu tunggu balasannya (request-response).
func (a *app) tcpLatency(ctx context.Context, run *Run, pr *ProtoResult, t0 time.Time) error {
	c, err := net.DialTimeout("tcp", a.addr(9000), 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	return pingPong(ctx, run, pr, t0, c, rd)
}

// quicLatency: satu koneksi QUIC, satu stream, pola yang sama dengan TCP.
func (a *app) quicLatency(ctx context.Context, run *Run, pr *ProtoResult, t0 time.Time) error {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(nil), quicConf)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "selesai")
	st, err := conn.OpenStreamSync(dctx)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.Write([]byte{'E'}); err != nil {
		return err
	}
	return pingPong(ctx, run, pr, t0, st, bufio.NewReader(st))
}

type deadlineRW interface {
	io.Writer
	SetDeadline(time.Time) error
}

func pingPong(ctx context.Context, run *Run, pr *ProtoResult, t0 time.Time, w deadlineRW, rd *bufio.Reader) error {
	p := run.Params
	for i := 0; i < p.Count; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := padMsg(fmt.Sprintf("%d ", i), p.Payload)
		start := time.Now()
		_ = w.SetDeadline(start.Add(30 * time.Second))
		run.mu.Lock()
		pr.Sent++
		run.mu.Unlock()
		if _, err := io.WriteString(w, msg); err != nil {
			return err
		}
		if _, err := rd.ReadString('\n'); err != nil {
			return err
		}
		rtt := time.Since(start)
		run.mu.Lock()
		pr.Samples = append(pr.Samples, Sample{Seq: i, RTT: ms(rtt), T: ms(start.Sub(t0))})
		run.mu.Unlock()
		sleepCtx(ctx, time.Duration(p.IntervalMs)*time.Millisecond)
	}
	return nil
}

// udpLatency: kirim datagram bernomor urut tanpa menunggu; yang tidak kembali dianggap hilang.
func (a *app) udpLatency(ctx context.Context, run *Run, pr *ProtoResult, t0 time.Time) error {
	raddr, err := net.ResolveUDPAddr("udp", a.addr(9001))
	if err != nil {
		return err
	}
	c, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return err
	}
	defer c.Close()
	p := run.Params
	sendAt := make([]time.Time, p.Count)
	got := make([]bool, p.Count)
	var mu sync.Mutex
	received := 0
	var maxRTT time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := c.Read(buf)
			now := time.Now()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					mu.Lock()
					fin := received == p.Count
					mu.Unlock()
					if fin || ctx.Err() != nil {
						return
					}
					continue
				}
				return
			}
			var seq int
			if _, err := fmt.Sscanf(string(buf[:n]), "U%d ", &seq); err != nil || seq < 0 || seq >= p.Count {
				continue
			}
			mu.Lock()
			if got[seq] || sendAt[seq].IsZero() {
				mu.Unlock()
				continue
			}
			got[seq] = true
			received++
			rtt := now.Sub(sendAt[seq])
			maxRTT = max(maxRTT, rtt)
			start := sendAt[seq]
			fin := received == p.Count
			mu.Unlock()
			run.mu.Lock()
			pr.Samples = append(pr.Samples, Sample{Seq: seq, RTT: ms(rtt), T: ms(start.Sub(t0))})
			run.mu.Unlock()
			if fin {
				return
			}
		}
	}()
	for i := 0; i < p.Count && ctx.Err() == nil; i++ {
		msg := padMsg(fmt.Sprintf("U%d ", i), p.Payload)
		mu.Lock()
		sendAt[i] = time.Now()
		mu.Unlock()
		run.mu.Lock()
		pr.Sent++
		run.mu.Unlock()
		_, _ = c.Write([]byte(msg))
		sleepCtx(ctx, time.Duration(p.IntervalMs)*time.Millisecond)
	}
	// tunggu balasan yang masih di jalan: 2 detik + 3x RTT terbesar yang terlihat
	mu.Lock()
	wait := 2*time.Second + 3*maxRTT
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(wait):
	}
	_ = c.Close()
	<-done
	mu.Lock()
	defer mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	for i := 0; i < pr.Sent; i++ {
		if !got[i] {
			pr.Samples = append(pr.Samples, Sample{Seq: i, Lost: true, T: ms(sendAt[i].Sub(t0))})
		}
	}
	sort.Slice(pr.Samples, func(i, j int) bool { return pr.Samples[i].Seq < pr.Samples[j].Seq })
	return nil
}

// --- Handshake ----------------------------------------------------------

func median(xs []float64) float64 { return Summarize(xs).Median }

// handshakes mengukur tiap jenis tiga kali dan mengambil median.
func (a *app) handshakes(ctx context.Context) []Handshake {
	type probe struct {
		name, theory, note string
		fn                 func(context.Context) (hs, first time.Duration, note string, err error)
	}
	probes := []probe{
		{"UDP", "0 RTT handshake, balasan 1 RTT", "tidak ada koneksi: datagram langsung dikirim", a.hsUDP},
		{"TCP", "1 RTT (SYN, SYN-ACK, ACK)", "", a.hsTCP},
		{"TCP + TLS 1.3", "2 RTT (TCP lalu TLS)", "", a.hsTLS},
		{"QUIC (baru)", "1 RTT (transport + TLS digabung)", "", a.hsQUIC},
		{"QUIC 0-RTT (koneksi ulang)", "0 RTT: data ikut packet pertama", "", a.hsQUIC0RTT},
	}
	var out []Handshake
	for _, p := range probes {
		h := Handshake{Name: p.name, TheoryRTT: p.theory, Note: p.note}
		var hsv, frv []float64
		for i := 0; i < 3 && ctx.Err() == nil; i++ {
			hs, fr, note, err := p.fn(ctx)
			if err != nil {
				h.Error = err.Error()
				continue
			}
			if note != "" {
				h.Note = note
			}
			hsv = append(hsv, ms(hs))
			frv = append(frv, ms(fr))
		}
		h.Runs = hsv
		if len(hsv) > 0 {
			h.HandshakeMs, h.FirstReplyMs, h.Error = median(hsv), median(frv), ""
		}
		out = append(out, h)
	}
	return out
}

func echoOnce(rw io.ReadWriter) error {
	if _, err := io.WriteString(rw, "hs\n"); err != nil {
		return err
	}
	_, err := bufio.NewReader(rw).ReadString('\n')
	return err
}

func (a *app) hsUDP(ctx context.Context) (time.Duration, time.Duration, string, error) {
	c, err := net.Dial("udp", a.addr(9001))
	if err != nil {
		return 0, 0, "", err
	}
	defer c.Close()
	buf := make([]byte, 64)
	for try := 0; try < 5; try++ {
		start := time.Now()
		_, _ = c.Write([]byte("U0 hs"))
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(buf); err == nil {
			return 0, time.Since(start), "", nil
		}
	}
	return 0, 0, "", errors.New("lima datagram berturut-turut hilang")
}

func (a *app) hsTCP(ctx context.Context) (time.Duration, time.Duration, string, error) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", a.addr(9000), 15*time.Second)
	if err != nil {
		return 0, 0, "", err
	}
	defer c.Close()
	hs := time.Since(start)
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := echoOnce(c); err != nil {
		return 0, 0, "", err
	}
	return hs, time.Since(start), "", nil
}

func (a *app) hsTLS(ctx context.Context) (time.Duration, time.Duration, string, error) {
	start := time.Now()
	raw, err := net.DialTimeout("tcp", a.addr(9003), 15*time.Second)
	if err != nil {
		return 0, 0, "", err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	conf := tlsClient(nil)
	conf.NextProtos = nil
	c := tls.Client(raw, conf)
	if err := c.HandshakeContext(ctx); err != nil {
		return 0, 0, "", err
	}
	hs := time.Since(start)
	if err := echoOnce(c); err != nil {
		return 0, 0, "", err
	}
	return hs, time.Since(start), "", nil
}

func (a *app) hsQUIC(ctx context.Context) (time.Duration, time.Duration, string, error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(nil), quicConf)
	if err != nil {
		return 0, 0, "", err
	}
	defer conn.CloseWithError(0, "")
	hs := time.Since(start)
	if err := quicEcho(dctx, conn); err != nil {
		return 0, 0, "", err
	}
	return hs, time.Since(start), "", nil
}

func quicEcho(ctx context.Context, conn *quic.Conn) error {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := st.Write([]byte("Ehs\n")); err != nil {
		return err
	}
	_, err = bufio.NewReader(st).ReadString('\n')
	return err
}

// hsQUIC0RTT: koneksi pertama menyimpan session ticket; koneksi kedua memakai
// DialAddrEarly sehingga data dikirim sebelum handshake selesai.
func (a *app) hsQUIC0RTT(ctx context.Context) (time.Duration, time.Duration, string, error) {
	cache := tls.NewLRUClientSessionCache(4)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	first, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(cache), quicConf)
	if err != nil {
		return 0, 0, "", err
	}
	if err := quicEcho(dctx, first); err != nil {
		first.CloseWithError(0, "")
		return 0, 0, "", err
	}
	time.Sleep(50 * time.Millisecond) // beri waktu session ticket tiba
	first.CloseWithError(0, "")

	start := time.Now()
	conn, err := quic.DialAddrEarly(dctx, a.addr(9002), tlsClient(cache), quicConf)
	if err != nil {
		return 0, 0, "", err
	}
	defer conn.CloseWithError(0, "")
	hs := time.Since(start)
	if err := quicEcho(dctx, conn); err != nil {
		return 0, 0, "", err
	}
	fr := time.Since(start)
	note := "0-RTT dipakai"
	select {
	case <-conn.HandshakeComplete():
		if !conn.ConnectionState().Used0RTT {
			note = "0-RTT ditolak server, jatuh ke 1-RTT"
		}
	case <-time.After(5 * time.Second):
	}
	return hs, fr, note, nil
}

// --- Throughput ---------------------------------------------------------

const tpLimit = 8 * time.Second

func (a *app) tcpThroughput(ctx context.Context, bulkKB int) Throughput {
	tp := Throughput{Note: "TCP: congestion control menyesuaikan laju; data yang hilang dikirim ulang"}
	start := time.Now()
	c, err := net.DialTimeout("tcp", a.addr(9004), 10*time.Second)
	if err != nil {
		tp.Error = err.Error()
		return tp
	}
	defer c.Close()
	tc := c.(*net.TCPConn)
	_ = tc.SetWriteBuffer(128 << 10) // antrean kirim kecil agar batas waktu berarti
	chunk := make([]byte, 32<<10)
	total := int64(bulkKB) << 10
	_ = c.SetWriteDeadline(start.Add(tpLimit))
	for tp.SentBytes < total && ctx.Err() == nil {
		n := min(int64(len(chunk)), total-tp.SentBytes)
		w, err := c.Write(chunk[:n])
		tp.SentBytes += int64(w)
		if err != nil {
			tp.Note += fmt.Sprintf("; berhenti setelah %v", tpLimit)
			break
		}
	}
	_ = tc.CloseWrite()
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		tp.Error = "tidak ada balasan sink: " + err.Error()
		return tp
	}
	tp.DeliveredBytes, _ = strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	el := time.Since(start)
	tp.Ms, tp.Mbps = ms(el), round(float64(tp.DeliveredBytes)*8/el.Seconds()/1e6)
	return tp
}

func (a *app) quicThroughput(ctx context.Context, bulkKB int) Throughput {
	tp := Throughput{Note: "QUIC: congestion control di user space (quic-go), andal per stream"}
	dctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := quic.DialAddr(dctx, a.addr(9002), tlsClient(nil), quicConf)
	if err != nil {
		tp.Error = err.Error()
		return tp
	}
	defer conn.CloseWithError(0, "")
	st, err := conn.OpenStreamSync(dctx)
	if err != nil {
		tp.Error = err.Error()
		return tp
	}
	_, _ = st.Write([]byte{'S'})
	chunk := make([]byte, 32<<10)
	total := int64(bulkKB) << 10
	_ = st.SetWriteDeadline(start.Add(tpLimit))
	for tp.SentBytes < total && ctx.Err() == nil {
		n := min(int64(len(chunk)), total-tp.SentBytes)
		w, err := st.Write(chunk[:n])
		tp.SentBytes += int64(w)
		if err != nil {
			tp.Note += fmt.Sprintf("; berhenti setelah %v", tpLimit)
			break
		}
	}
	_ = st.SetWriteDeadline(time.Time{})
	_ = st.Close() // tutup arah kirim (FIN stream)
	_ = st.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(st).ReadString('\n')
	if err != nil {
		tp.Error = "tidak ada balasan sink: " + err.Error()
		return tp
	}
	tp.DeliveredBytes, _ = strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	el := time.Since(start)
	tp.Ms, tp.Mbps = ms(el), round(float64(tp.DeliveredBytes)*8/el.Seconds()/1e6)
	return tp
}

// udpThroughput: kirim secepat mungkin tanpa congestion control; server menghitung yang tiba.
func (a *app) udpThroughput(ctx context.Context, bulkKB int) Throughput {
	tp := Throughput{Note: "UDP: tidak ada congestion control dan retransmission; yang hilang tetap hilang"}
	c, err := net.Dial("udp", a.addr(9001))
	if err != nil {
		tp.Error = err.Error()
		return tp
	}
	defer c.Close()
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	const size = 1200
	pkt := []byte(padMsg("SINK:"+id+":", size))
	count := (bulkKB<<10 + size - 1) / size
	start := time.Now()
	for i := 0; i < count && ctx.Err() == nil; i++ {
		if _, err := c.Write(pkt); err == nil {
			tp.SentBytes += size
		}
	}
	// tanya server berulang sampai jumlah yang tiba tidak bertambah lagi
	var cnt, byt, first, last int64
	prev := int64(-1)
	buf := make([]byte, 256)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(700 * time.Millisecond)
		_, _ = c.Write([]byte("STAT:" + id))
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimSpace(string(buf[:n])), ":")
		if len(parts) != 6 {
			continue
		}
		cnt, _ = strconv.ParseInt(parts[2], 10, 64)
		byt, _ = strconv.ParseInt(parts[3], 10, 64)
		first, _ = strconv.ParseInt(parts[4], 10, 64)
		last, _ = strconv.ParseInt(parts[5], 10, 64)
		if cnt == prev || cnt == int64(count) {
			break
		}
		prev = cnt
	}
	tp.DeliveredBytes = byt
	if count > 0 {
		tp.LossPct = round(100 * float64(int64(count)-cnt) / float64(count))
	}
	el := time.Duration(last-first) * time.Microsecond
	if el <= 0 {
		el = time.Since(start)
	}
	tp.Ms = ms(el)
	if el > 0 {
		tp.Mbps = round(float64(byt) * 8 / el.Seconds() / 1e6)
	}
	return tp
}
