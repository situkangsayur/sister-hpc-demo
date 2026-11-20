package clock

import "testing"

// Skenario Gambar Lamport di buku: P1 lokal(1), kirim(2), lokal(3); P2 lokal(1), terima(3), kirim(4);
// P3 lokal(1), terima(5), lokal(6).
func TestLamportSkenarioBuku(t *testing.T) {
	var p1, p2, p3 Lamport
	if p1.Local() != 1 || p2.Local() != 1 || p3.Local() != 1 {
		t.Fatal("kejadian lokal pertama harus 1")
	}
	m := p1.Send()
	if m != 2 {
		t.Fatalf("kirim P1 = %d, mau 2", m)
	}
	if got := p2.Recv(m); got != 3 {
		t.Fatalf("terima P2 = %d, mau 3", got)
	}
	if got := p1.Local(); got != 3 {
		t.Fatalf("lokal P1 = %d, mau 3", got)
	}
	m2 := p2.Send()
	if got := p3.Recv(m2); got != 5 {
		t.Fatalf("terima P3 = %d, mau 5", got)
	}
	if got := p3.Local(); got != 6 {
		t.Fatalf("lokal P3 = %d, mau 6", got)
	}
}

func TestVectorKonflik(t *testing.T) {
	// Contoh buku: A [1,0,0] tersalin ke B, B menulis [1,1,0]; C menulis [0,0,1].
	a := NewVector(3)
	a.Tick(0)
	b := NewVector(3)
	b.Merge(a, 1) // terima dari A lalu tulis
	if Compare(a, b) != Before {
		t.Fatalf("a mestinya sebelum b, dapat %s", Compare(a, b))
	}
	c := NewVector(3)
	c.Tick(2)
	if Compare(b, c) != Concurrent {
		t.Fatalf("b dan c mestinya concurrent, dapat %s", Compare(b, c))
	}
	if Compare(c, c.Copy()) != Equal {
		t.Fatal("vektor sama mestinya Equal")
	}
	if Compare(b, a) != After {
		t.Fatal("b mestinya sesudah a")
	}
}
