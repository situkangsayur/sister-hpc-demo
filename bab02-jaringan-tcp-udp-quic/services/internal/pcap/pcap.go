// Package pcap menjalankan tcpdump di dalam container dan mengurai hasilnya
// (Bab 2, subbab "Mengamati Packet").
//
// Prober menangkap packet di sisi klien, echo menangkap di sisi server.
// Karena router netem berada di tengah, packet yang dibuang netem terlihat
// di tangkapan pengirim tetapi tidak pernah muncul di tangkapan penerima.
package pcap

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Packet adalah satu baris tcpdump yang sudah diurai.
type Packet struct {
	TS      float64 `json:"ts"` // detik Unix (jam host yang sama untuk semua container)
	Src     string  `json:"src"`
	SrcPort int     `json:"src_port"`
	Dst     string  `json:"dst"`
	DstPort int     `json:"dst_port"`
	Proto   string  `json:"proto"` // tcp | udp
	Flags   string  `json:"flags,omitempty"`
	Kind    string  `json:"kind"`
	Seq     string  `json:"seq,omitempty"`
	Ack     string  `json:"ack,omitempty"`
	Len     int     `json:"len"`
	Info    string  `json:"info"`
	key     string  // header transport + awal payload, untuk mencocokkan dua tangkapan
}

var (
	reHead  = regexp.MustCompile(`^(\d+\.\d+) IP (\S+) > (\S+): (.*)$`)
	reFlags = regexp.MustCompile(`Flags \[([^\]]*)\]`)
	reSeq   = regexp.MustCompile(`seq (\d+(?::\d+)?)`)
	reAck   = regexp.MustCompile(`ack (\d+)`)
	reLen   = regexp.MustCompile(`length (\d+)`)
	reHex   = regexp.MustCompile(`^\s*0x[0-9a-f]+:\s+(.*)$`)
)

func splitHostPort(s string) (string, int) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return s, 0
	}
	p, _ := strconv.Atoi(strings.TrimSuffix(s[i+1:], ":"))
	return s[:i], p
}

// TCPKind menerjemahkan flag tcpdump menjadi nama yang dipakai di buku.
func TCPKind(flags string, length int) string {
	switch {
	case strings.Contains(flags, "R"):
		return "RST"
	case strings.Contains(flags, "S") && strings.Contains(flags, "."):
		return "SYN-ACK"
	case strings.Contains(flags, "S"):
		return "SYN"
	case strings.Contains(flags, "F"):
		return "FIN"
	case length > 0:
		return "DATA"
	default:
		return "ACK"
	}
}

// quicKind membaca byte pertama payload UDP: long header (Initial, 0-RTT,
// Handshake, Retry) atau short header (1-RTT).
func quicKind(b byte) string {
	if b&0x80 == 0 {
		return "QUIC 1-RTT"
	}
	switch (b & 0x30) >> 4 {
	case 0:
		return "QUIC Initial"
	case 1:
		return "QUIC 0-RTT"
	case 2:
		return "QUIC Handshake"
	default:
		return "QUIC Retry"
	}
}

// Parse mengurai keluaran `tcpdump -nn -tt -S -x`. quicPort menandai port UDP
// yang dianggap QUIC.
func Parse(out string, quicPort int) []Packet {
	var pkts []Packet
	var cur *Packet
	var raw []byte
	flush := func() {
		if cur == nil {
			return
		}
		finishPacket(cur, raw, quicPort)
		pkts = append(pkts, *cur)
		cur, raw = nil, nil
	}
	for _, line := range strings.Split(out, "\n") {
		if m := reHex.FindStringSubmatch(line); m != nil && cur != nil {
			b, _ := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(m[1]), " ", ""))
			raw = append(raw, b...)
			continue
		}
		m := reHead.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		flush()
		ts, _ := strconv.ParseFloat(m[1], 64)
		p := Packet{TS: ts, Info: m[4]}
		p.Src, p.SrcPort = splitHostPort(m[2])
		p.Dst, p.DstPort = splitHostPort(m[3])
		if lm := reLen.FindAllStringSubmatch(m[4], -1); lm != nil {
			p.Len, _ = strconv.Atoi(lm[len(lm)-1][1])
		}
		if fm := reFlags.FindStringSubmatch(m[4]); fm != nil {
			p.Proto, p.Flags = "tcp", fm[1]
			if sm := reSeq.FindStringSubmatch(m[4]); sm != nil {
				p.Seq = sm[1]
			}
			if am := reAck.FindStringSubmatch(m[4]); am != nil {
				p.Ack = am[1]
			}
			p.Kind = TCPKind(p.Flags, p.Len)
		} else {
			p.Proto, p.Kind = "udp", "UDP"
		}
		cur = &p
	}
	flush()
	return pkts
}

func finishPacket(p *Packet, raw []byte, quicPort int) {
	if len(raw) >= 20 {
		ihl := int(raw[0]&0x0f) * 4
		if ihl < len(raw) {
			tp := raw[ihl:]
			if len(tp) > 48 {
				tp = tp[:48]
			}
			p.key = fmt.Sprintf("%s>%s|%d|%x", p.Src, p.Dst, p.Len, tp)
			if p.Proto == "udp" && (p.SrcPort == quicPort || p.DstPort == quicPort) && len(raw) > ihl+8 {
				p.Kind = quicKind(raw[ihl+8])
			}
		}
	}
	if p.key == "" {
		p.key = fmt.Sprintf("%s>%s|%d|%s|%s|%s", p.Src, p.Dst, p.Len, p.Flags, p.Seq, p.Ack)
	}
}

// Flow adalah satu packet yang dilihat dari dua sisi: kapan dikirim, kapan tiba.
type Flow struct {
	Dir     string   `json:"dir"` // c2s | s2c
	SendTS  *float64 `json:"send_ts"`
	RecvTS  *float64 `json:"recv_ts"` // nil = tidak pernah tiba (dibuang netem)
	Kind    string   `json:"kind"`
	Flags   string   `json:"flags,omitempty"`
	Seq     string   `json:"seq,omitempty"`
	Ack     string   `json:"ack,omitempty"`
	Len     int      `json:"len"`
	Info    string   `json:"info"`
	Retrans bool     `json:"retrans"`
}

// Match mencocokkan tangkapan sisi klien dan sisi server. serverPort dipakai
// untuk menentukan arah packet.
func Match(client, server []Packet, serverPort int) []Flow {
	used := make([]bool, len(server))
	var flows []Flow
	seen := map[string]bool{}
	addRetrans := func(f *Flow) {
		if f.Seq == "" || (f.Len == 0 && f.Kind != "SYN" && f.Kind != "FIN" && f.Kind != "SYN-ACK") {
			return
		}
		k := f.Dir + "|" + f.Kind + "|" + f.Seq
		f.Retrans = seen[k]
		seen[k] = true
	}
	// tangkapan klien: c2s = dikirim, s2c = diterima
	for _, cp := range client {
		cp := cp
		j := -1
		for i := range server {
			if !used[i] && server[i].key == cp.key {
				j = i
				break
			}
		}
		f := Flow{Kind: cp.Kind, Flags: cp.Flags, Seq: cp.Seq, Ack: cp.Ack, Len: cp.Len, Info: cp.Info}
		var sts *float64
		if j >= 0 {
			used[j] = true
			v := server[j].TS
			sts = &v
		}
		ts := cp.TS
		if cp.DstPort == serverPort {
			f.Dir, f.SendTS, f.RecvTS = "c2s", &ts, sts
		} else {
			f.Dir, f.SendTS, f.RecvTS = "s2c", sts, &ts
		}
		flows = append(flows, f)
	}
	// sisa tangkapan server: s2c yang tidak pernah tiba di klien
	for i, sp := range server {
		if used[i] {
			continue
		}
		ts := sp.TS
		f := Flow{Kind: sp.Kind, Flags: sp.Flags, Seq: sp.Seq, Ack: sp.Ack, Len: sp.Len, Info: sp.Info}
		if sp.SrcPort == serverPort {
			f.Dir, f.SendTS = "s2c", &ts
		} else {
			f.Dir, f.RecvTS = "c2s", &ts // seharusnya tidak terjadi
		}
		flows = append(flows, f)
	}
	sortFlows(flows)
	for i := range flows {
		addRetrans(&flows[i])
	}
	return flows
}

func flowTime(f Flow) float64 {
	if f.SendTS != nil {
		return *f.SendTS
	}
	return *f.RecvTS
}

func sortFlows(fs []Flow) {
	// insertion sort cukup: puluhan packet saja
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && flowTime(fs[j]) < flowTime(fs[j-1]); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

// Capture adalah satu proses tcpdump yang sedang berjalan.
type Capture struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	buf    strings.Builder
	done   chan struct{}
	cancel context.CancelFunc
}

// Start menjalankan tcpdump dan menunggu sampai siap ("listening on ...").
func Start(iface string, filter []string, maxDur time.Duration) (*Capture, error) {
	ctx, cancel := context.WithTimeout(context.Background(), maxDur)
	args := append([]string{"-i", iface, "-nn", "-tt", "-S", "-x", "-s", "128", "-l", "-c", "3000"}, filter...)
	cmd := exec.CommandContext(ctx, "tcpdump", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	c := &Capture{cmd: cmd, done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(c.done)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			c.mu.Lock()
			c.buf.WriteString(sc.Text())
			c.buf.WriteByte('\n')
			c.mu.Unlock()
		}
	}()
	ready := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		sent := false
		for sc.Scan() {
			if !sent && strings.Contains(sc.Text(), "listening on") {
				ready <- nil
				sent = true
			}
		}
		if !sent {
			ready <- errors.New("tcpdump berhenti sebelum siap")
		}
		_, _ = io.Copy(io.Discard, stderr)
	}()
	select {
	case err := <-ready:
		if err != nil {
			cancel()
			return nil, err
		}
	case <-time.After(5 * time.Second):
		cancel()
		return nil, errors.New("tcpdump tidak siap dalam 5 detik")
	}
	return c, nil
}

// Stop menghentikan tcpdump dengan SIGINT dan mengembalikan seluruh keluarannya.
func (c *Capture) Stop() string {
	time.Sleep(200 * time.Millisecond)
	_ = c.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		c.cancel()
		<-c.done
	}
	_ = c.cmd.Wait()
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Summary mengembalikan baris kepala packet saja (tanpa hex) untuk ditampilkan.
func Summary(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if reHead.MatchString(l) {
			lines = append(lines, l)
		}
	}
	return lines
}
