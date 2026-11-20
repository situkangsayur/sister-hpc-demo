package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

// CertInfo adalah ringkasan satu X.509 certificate untuk ditampilkan di UI.
type CertInfo struct {
	Role      string   `json:"role"`
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	Serial    string   `json:"serial"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	DNSNames  []string `json:"dns_names,omitempty"`
	IPs       []string `json:"ips,omitempty"`
	IsCA      bool     `json:"is_ca"`
	KeyAlg    string   `json:"key_alg"`
	SigAlg    string   `json:"sig_alg"`
	SHA256    string   `json:"sha256"`
}

func certInfo(c *x509.Certificate) CertInfo {
	sum := sha256.Sum256(c.Raw)
	role := "leaf"
	if c.IsCA {
		role = "intermediate"
		if c.Subject.String() == c.Issuer.String() {
			role = "root"
		}
	}
	ci := CertInfo{
		Role: role, Subject: c.Subject.CommonName, Issuer: c.Issuer.CommonName,
		Serial: c.SerialNumber.Text(16), NotBefore: c.NotBefore.UTC().Format(time.RFC3339),
		NotAfter: c.NotAfter.UTC().Format(time.RFC3339), DNSNames: c.DNSNames, IsCA: c.IsCA,
		KeyAlg: c.PublicKeyAlgorithm.String(), SigAlg: c.SignatureAlgorithm.String(),
		SHA256: hex.EncodeToString(sum[:8]) + "…",
	}
	if k, ok := c.PublicKey.(*ecdsa.PublicKey); ok {
		ci.KeyAlg += " " + k.Curve.Params().Name
	}
	for _, ip := range c.IPAddresses {
		ci.IPs = append(ci.IPs, ip.String())
	}
	return ci
}

// Check adalah hasil satu uji verifikasi (ok atau pesan error).
type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Msg  string `json:"msg"`
}

// TLSReport adalah hasil menengok satu endpoint TLS.
type TLSReport struct {
	Target      string     `json:"target"`
	Addr        string     `json:"addr"`
	Version     string     `json:"version"`
	Cipher      string     `json:"cipher"`
	ALPN        string     `json:"alpn"`
	ConnectMs   float64    `json:"connect_ms"`
	HandshakeMs float64    `json:"handshake_ms"`
	PeerChain   []CertInfo `json:"peer_chain"`
	Verified    []CertInfo `json:"verified_chain"`
	Checks      []Check    `json:"checks"`
	Err         string     `json:"err,omitempty"`
}

// InspectTLS membuka koneksi TLS ke addr dan melaporkan versi, cipher suite, ALPN, dan rantai certificate.
func InspectTLS(ctx context.Context, target, addr, sni string, maxVer uint16, roots *x509.CertPool) TLSReport {
	rep := TLSReport{Target: target, Addr: addr}
	d := net.Dialer{Timeout: 5 * time.Second}
	t0 := time.Now()
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	defer raw.Close()
	rep.ConnectMs = float64(time.Since(t0).Microseconds()) / 1000
	conn := tls.Client(raw, &tls.Config{
		ServerName: sni, RootCAs: roots, MaxVersion: maxVer,
		NextProtos: []string{"h2", "http/1.1"},
	})
	t1 := time.Now()
	err = conn.HandshakeContext(ctx)
	rep.HandshakeMs = float64(time.Since(t1).Microseconds()) / 1000
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	st := conn.ConnectionState()
	rep.Version = tls.VersionName(st.Version)
	rep.Cipher = tls.CipherSuiteName(st.CipherSuite)
	rep.ALPN = st.NegotiatedProtocol
	for _, c := range st.PeerCertificates {
		rep.PeerChain = append(rep.PeerChain, certInfo(c))
	}
	if len(st.VerifiedChains) > 0 {
		for _, c := range st.VerifiedChains[0] {
			rep.Verified = append(rep.Verified, certInfo(c))
		}
	}
	rep.Checks = verifyChecks(st.PeerCertificates, sni, roots)
	return rep
}

// verifyChecks memperlihatkan mengapa browser percaya atau menolak certificate yang sama.
func verifyChecks(chain []*x509.Certificate, sni string, roots *x509.CertPool) []Check {
	if len(chain) == 0 {
		return nil
	}
	leaf := chain[0]
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	run := func(name string, opts x509.VerifyOptions) Check {
		opts.Intermediates = inter
		if _, err := leaf.Verify(opts); err != nil {
			return Check{Name: name, OK: false, Msg: err.Error()}
		}
		return Check{Name: name, OK: true, Msg: "rantai valid sampai root"}
	}
	sys, err := x509.SystemCertPool()
	if err != nil || sys == nil {
		sys = x509.NewCertPool()
	}
	return []Check{
		run("Dengan root CA demo (dipasang di trust store probe)", x509.VerifyOptions{DNSName: sni, Roots: roots}),
		run("Dengan trust store sistem saja (seperti browser Anda)", x509.VerifyOptions{DNSName: sni, Roots: sys}),
		run("Nama host lain: bank-palsu.example", x509.VerifyOptions{DNSName: "bank-palsu.example", Roots: roots}),
		run("Seandainya hari ini 30 hari lagi (leaf berumur 7 hari)", x509.VerifyOptions{DNSName: sni, Roots: roots,
			CurrentTime: time.Now().AddDate(0, 0, 30)}),
	}
}

// MTLSAttempt adalah hasil satu percobaan memanggil endpoint mTLS.
type MTLSAttempt struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	Err    string `json:"err,omitempty"`
}

// rogueCert membuat certificate self-signed "penyusup" yang tidak diterbitkan CA demo.
func rogueCert() (tls.Certificate, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "penyusup"},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, nil
}

// TryMTLS memanggil endpoint mTLS tiga kali: dengan certificate sah, tanpa certificate, dan dengan certificate penyusup.
func TryMTLS(ctx context.Context, url string, roots *x509.CertPool, client *tls.Certificate) []MTLSAttempt {
	rogue, rerr := rogueCert()
	type variant struct {
		name  string
		certs []tls.Certificate
	}
	vs := []variant{{"layanan-pesanan (certificate dari CA demo)", nil}, {"tanpa certificate klien", nil}, {"penyusup (self-signed, bukan dari CA demo)", nil}}
	if client != nil {
		vs[0].certs = []tls.Certificate{*client}
	}
	if rerr == nil {
		vs[2].certs = []tls.Certificate{rogue}
	}
	var out []MTLSAttempt
	for _, v := range vs {
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: v.certs}}
		cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		a := MTLSAttempt{Name: v.name}
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		res, err := cl.Do(req)
		if err != nil {
			a.Err = simplifyErr(err)
		} else {
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			a.Status, a.Body, a.OK = res.StatusCode, strings.TrimSpace(string(b)), res.StatusCode == 200
		}
		tr.CloseIdleConnections()
		out = append(out, a)
	}
	return out
}

func simplifyErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": remote error: "); i >= 0 {
		return "handshake ditolak server" + s[i:]
	}
	return s
}

// tlsVersion menerjemahkan "1.2"/"1.3" menjadi konstanta crypto/tls.
func tlsVersion(s string) (uint16, error) {
	switch s {
	case "", "1.3":
		return tls.VersionTLS13, nil
	case "1.2":
		return tls.VersionTLS12, nil
	}
	return 0, fmt.Errorf("versi TLS %q tidak didukung (pakai 1.2 atau 1.3)", s)
}
