package main

import (
	"crypto/x509"
	"testing"
	"time"
)

// TestChainVerifies memastikan leaf server terverifikasi lewat intermediate ke root.
func TestChainVerifies(t *testing.T) {
	b, err := Generate([]string{"origin", "127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(b.Root)
	inter := x509.NewCertPool()
	inter.AddCert(b.Inter)
	chains, err := b.Server.Verify(x509.VerifyOptions{DNSName: "origin", Roots: roots, Intermediates: inter})
	if err != nil {
		t.Fatalf("verifikasi gagal: %v", err)
	}
	if len(chains[0]) != 3 {
		t.Fatalf("panjang rantai = %d, mau 3", len(chains[0]))
	}
	// Tanpa intermediate, verifikasi harus gagal: rantai terputus.
	if _, err := b.Server.Verify(x509.VerifyOptions{DNSName: "origin", Roots: roots}); err == nil {
		t.Fatal("verifikasi tanpa intermediate seharusnya gagal")
	}
	// Klien mTLS harus berstatus ClientAuth.
	if _, err := b.Client.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("certificate klien: %v", err)
	}
	if b.Server.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatal("SAN IP tidak terpasang")
	}
}
