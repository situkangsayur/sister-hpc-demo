package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tapConn mencatat semua byte yang lewat di satu koneksi TCP, seperti Wireshark yang sangat sederhana.
type tapConn struct {
	net.Conn
	mu     sync.Mutex
	chunks []Chunk
}

// Chunk adalah potongan byte yang dikirim (out) atau diterima (in).
type Chunk struct {
	Dir  string
	Data []byte
}

func (c *tapConn) record(dir string, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.chunks)
	if n > 0 && c.chunks[n-1].Dir == dir {
		c.chunks[n-1].Data = append(c.chunks[n-1].Data, p...)
		return
	}
	c.chunks = append(c.chunks, Chunk{Dir: dir, Data: append([]byte(nil), p...)})
}

func (c *tapConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.record("in", p[:n])
	}
	return n, err
}

func (c *tapConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.record("out", p[:n])
	}
	return n, err
}

// Record adalah satu TLS record (atau satu potongan teks pada HTTP polos) untuk UI.
type Record struct {
	Dir     string `json:"dir"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail,omitempty"`
	Length  int    `json:"length"`
	Hex     string `json:"hex"`
	ASCII   string `json:"ascii"`
	Visible string `json:"visible,omitempty"`
}

// SniffReport membandingkan apa yang terlihat oleh penyadap di jaringan.
type SniffReport struct {
	Mode          string   `json:"mode"`
	URL           string   `json:"url"`
	Status        int      `json:"status"`
	ResponseBody  string   `json:"response_body"`
	BytesOut      int      `json:"bytes_out"`
	BytesIn       int      `json:"bytes_in"`
	SecretVisible bool     `json:"secret_visible"`
	SNI           string   `json:"sni,omitempty"`
	Records       []Record `json:"records"`
	Err           string   `json:"err,omitempty"`
}

const secret = "rahasia123"

func printable(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 32 && c < 127 {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func hexPreview(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	return hex.EncodeToString(b)
}

var contentTypes = map[byte]string{20: "ChangeCipherSpec", 21: "Alert", 22: "Handshake", 23: "ApplicationData"}
var handshakeTypes = map[byte]string{1: "ClientHello", 2: "ServerHello", 4: "NewSessionTicket", 8: "EncryptedExtensions",
	11: "Certificate", 12: "ServerKeyExchange", 13: "CertificateRequest", 14: "ServerHelloDone", 15: "CertificateVerify",
	16: "ClientKeyExchange", 20: "Finished"}

// ParseTLSRecords memecah aliran byte satu arah menjadi TLS record (5 byte header: type, version, length).
func ParseTLSRecords(dir string, stream []byte, encrypted bool) (recs []Record, sni string) {
	for len(stream) >= 5 {
		typ := stream[0]
		ln := int(binary.BigEndian.Uint16(stream[3:5]))
		if len(stream) < 5+ln {
			ln = len(stream) - 5
		}
		payload := stream[5 : 5+ln]
		r := Record{Dir: dir, Kind: contentTypes[typ], Length: ln, Hex: hexPreview(stream[:5+ln], 40), ASCII: printable(payload, 64)}
		if r.Kind == "" {
			r.Kind = "tidak dikenal"
		}
		// Handshake yang belum terenkripsi (ClientHello, ServerHello, dan seluruh handshake TLS 1.2).
		if typ == 20 {
			encrypted = true // sesudah ChangeCipherSpec, isi handshake berikutnya terenkripsi
		}
		if typ == 22 && len(payload) > 0 && !encrypted {
			r.Detail = handshakeTypes[payload[0]]
			if payload[0] == 1 {
				if s := clientHelloSNI(payload); s != "" {
					sni = s
					r.Detail += " (SNI: " + s + ")"
				}
			}
		}
		if typ == 22 && encrypted {
			r.Detail = "Finished (terenkripsi)"
		}
		if typ == 23 {
			r.Detail = "terenkripsi (AEAD)"
		}
		recs = append(recs, r)
		stream = stream[5+ln:]
	}
	return recs, sni
}

// clientHelloSNI mengambil server_name dari ClientHello: SNI dikirim polos, penyadap dapat membacanya.
func clientHelloSNI(hs []byte) string {
	p := hs
	if len(p) < 4 {
		return ""
	}
	p = p[4:] // type(1) + length(3)
	if len(p) < 2+32+1 {
		return ""
	}
	p = p[2+32:] // version + random
	sid := int(p[0])
	if len(p) < 1+sid+2 {
		return ""
	}
	p = p[1+sid:]
	cs := int(binary.BigEndian.Uint16(p))
	if len(p) < 2+cs+1 {
		return ""
	}
	p = p[2+cs:]
	cm := int(p[0])
	if len(p) < 1+cm+2 {
		return ""
	}
	p = p[1+cm:]
	extLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if len(p) > extLen {
		p = p[:extLen]
	}
	for len(p) >= 4 {
		et := binary.BigEndian.Uint16(p)
		el := int(binary.BigEndian.Uint16(p[2:]))
		if len(p) < 4+el {
			return ""
		}
		d := p[4 : 4+el]
		if et == 0 && len(d) >= 5 { // server_name
			nl := int(binary.BigEndian.Uint16(d[3:5]))
			if len(d) >= 5+nl {
				return string(d[5 : 5+nl])
			}
		}
		p = p[4+el:]
	}
	return ""
}

// Sniff mengirim form login lewat HTTP polos atau HTTPS sambil menyadap byte di koneksi TCP.
func Sniff(ctx context.Context, mode, target string, roots *x509.CertPool) SniffReport {
	rep := SniffReport{Mode: mode, URL: target}
	var tap *tapConn
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			tap = &tapConn{Conn: c}
			return tap, nil
		},
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		Protocols:         h1Only(),
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	form := url.Values{"user": {"budi"}, "password": {secret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "sesi=abc123; tema=gelap")
	res, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	rep.Status = res.StatusCode
	rep.ResponseBody = strings.TrimSpace(string(body))
	if tap == nil {
		return rep
	}
	tap.mu.Lock()
	chunks := append([]Chunk(nil), tap.chunks...)
	tap.mu.Unlock()
	var out, in []byte
	for _, c := range chunks {
		if c.Dir == "out" {
			out = append(out, c.Data...)
		} else {
			in = append(in, c.Data...)
		}
	}
	rep.BytesOut, rep.BytesIn = len(out), len(in)
	rep.SecretVisible = bytes.Contains(out, []byte(secret)) || bytes.Contains(in, []byte(secret))
	if mode == "http" {
		for _, c := range chunks {
			rep.Records = append(rep.Records, Record{Dir: c.Dir, Kind: "teks HTTP", Length: len(c.Data),
				Hex: hexPreview(c.Data, 40), Visible: printableKeepNL(c.Data, 1500)})
		}
		return rep
	}
	// HTTPS: urai record per arah lalu gabungkan sesuai urutan kemunculan chunk.
	encOut, encIn := false, false
	for _, c := range chunks {
		var recs []Record
		var sni string
		if c.Dir == "out" {
			recs, sni = ParseTLSRecords("out", c.Data, encOut)
			encOut = encOut || hasKind(recs, "ChangeCipherSpec")
		} else {
			recs, sni = ParseTLSRecords("in", c.Data, encIn)
			encIn = encIn || hasKind(recs, "ChangeCipherSpec")
		}
		if sni != "" {
			rep.SNI = sni
		}
		rep.Records = append(rep.Records, recs...)
	}
	return rep
}

func hasKind(rs []Record, k string) bool {
	for _, r := range rs {
		if r.Kind == k {
			return true
		}
	}
	return false
}

func printableKeepNL(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		switch {
		case c == '\n':
			out = append(out, c)
		case c == '\r':
		case c >= 32 && c < 127:
			out = append(out, c)
		default:
			out = append(out, '.')
		}
	}
	return string(out)
}

// h1Only membatasi Transport ke HTTP/1.1 (ALPN http/1.1) agar isi request mudah dibandingkan.
func h1Only() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	return p
}
