// certgen membuat CA demo dan certificate untuk Bab 3 saat runtime.
//
// Rantai yang dibuat (Bab 3, subbab "X.509 certificate dan rantai kepercayaan"):
//
//	Root CA "Sister Demo Root CA"            (ca.pem, self-signed, 365 hari)
//	  └─ Intermediate "Sister Demo Issuing CA" (intermediate.pem, 30 hari)
//	       ├─ leaf server  (server.pem = leaf + intermediate, server-key.pem, 7 hari)
//	       └─ leaf klien mTLS "layanan-pesanan" (client.pem, client-key.pem)
//
// Tidak ada kunci yang di-commit ke repo: semuanya lahir di volume Docker dan
// hilang bersama `docker compose down -v`. Kunci privat CA tidak disimpan.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bundle berisi semua certificate yang diterbitkan.
type Bundle struct {
	Root, Inter, Server, Client             *x509.Certificate
	RootDER, InterDER, ServerDER, ClientDER []byte
	ServerKey, ClientKey                    *ecdsa.PrivateKey
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	return n
}

func sign(tpl, parent *x509.Certificate, pub crypto.PublicKey, priv crypto.Signer) (*x509.Certificate, []byte, error) {
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, pub, priv)
	if err != nil {
		return nil, nil, err
	}
	c, err := x509.ParseCertificate(der)
	return c, der, err
}

// splitSAN memisah daftar nama menjadi DNS name dan IP address.
func splitSAN(names []string) (dns []string, ips []net.IP) {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, n)
		}
	}
	return
}

// Generate membuat root CA, intermediate, leaf server, dan leaf klien.
func Generate(sans []string, now time.Time) (*Bundle, error) {
	b := &Bundle{}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	rootTpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Sister Demo Root CA", Organization: []string{"STMIK Tazkia (demo)"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if b.Root, b.RootDER, err = sign(rootTpl, rootTpl, &rootKey.PublicKey, rootKey); err != nil {
		return nil, err
	}

	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	interTpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Sister Demo Issuing CA", Organization: []string{"STMIK Tazkia (demo)"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, 30),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	if b.Inter, b.InterDER, err = sign(interTpl, b.Root, &interKey.PublicKey, rootKey); err != nil {
		return nil, err
	}

	if b.ServerKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	dns, ips := splitSAN(sans)
	srvTpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "origin.sister-bab03", Organization: []string{"STMIK Tazkia (demo)"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, 7),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	if b.Server, b.ServerDER, err = sign(srvTpl, b.Inter, &b.ServerKey.PublicKey, interKey); err != nil {
		return nil, err
	}

	if b.ClientKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	cliTpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "layanan-pesanan", Organization: []string{"STMIK Tazkia (demo)"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, 7),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if b.Client, b.ClientDER, err = sign(cliTpl, b.Inter, &b.ClientKey.PublicKey, interKey); err != nil {
		return nil, err
	}
	return b, nil
}

func pemCert(der ...[]byte) []byte {
	var out []byte
	for _, d := range der {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	return out
}

func pemKey(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// Write menulis bundle ke dir. server.pem berisi leaf lalu intermediate (fullchain).
func (b *Bundle) Write(dir string) error {
	sk, err := pemKey(b.ServerKey)
	if err != nil {
		return err
	}
	ck, err := pemKey(b.ClientKey)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"ca.pem":           pemCert(b.RootDER),
		"intermediate.pem": pemCert(b.InterDER),
		"server.pem":       pemCert(b.ServerDER, b.InterDER),
		"server-key.pem":   sk,
		"client.pem":       pemCert(b.ClientDER, b.InterDER),
		"client-key.pem":   ck,
	}
	for name, data := range files {
		// 0644 agar nginx (user non-root) dapat membaca; ini kunci demo saja.
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// stillValid memeriksa apakah server.pem di dir masih berlaku lebih dari satu hari.
func stillValid(dir string, now time.Time) bool {
	data, err := os.ReadFile(filepath.Join(dir, "server.pem"))
	if err != nil {
		return false
	}
	blk, _ := pem.Decode(data)
	if blk == nil {
		return false
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return false
	}
	return c.NotAfter.Sub(now) > 24*time.Hour
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "certgen")
	dir := os.Getenv("CERT_DIR")
	if dir == "" {
		dir = "/certs"
	}
	sans := strings.Split(os.Getenv("CERT_SANS"), ",")
	now := time.Now()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Error("mkdir gagal", "err", err)
		os.Exit(1)
	}
	if stillValid(dir, now) && os.Getenv("FORCE") == "" {
		log.Info("certificate masih berlaku, tidak dibuat ulang", "dir", dir)
		return
	}
	b, err := Generate(sans, now)
	if err == nil {
		err = b.Write(dir)
	}
	if err != nil {
		log.Error("gagal membuat certificate", "err", err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.pem")); errors.Is(err, os.ErrNotExist) {
		log.Error("server.pem tidak ada sesudah ditulis")
		os.Exit(1)
	}
	log.Info("certificate dibuat", "dir", dir, "sans", fmt.Sprint(b.Server.DNSNames, b.Server.IPAddresses),
		"leaf_not_after", b.Server.NotAfter.Format(time.RFC3339))
}
