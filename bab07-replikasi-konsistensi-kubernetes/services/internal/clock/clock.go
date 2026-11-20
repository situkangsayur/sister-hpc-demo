// Package clock berisi Lamport clock dan vector clock (Bab 7, subbab "Waktu dan Urutan").
package clock

// Lamport adalah penghitung logis satu proses (Algoritma Lamport 1978).
type Lamport struct{ C int }

// Local dipanggil untuk kejadian lokal: C <- C + 1.
func (l *Lamport) Local() int { l.C++; return l.C }

// Send dipanggil sebelum mengirim message: C <- C + 1, nilai C ikut dikirim.
func (l *Lamport) Send() int { l.C++; return l.C }

// Recv dipanggil saat menerima message bercap t: C <- max(C, t) + 1.
func (l *Lamport) Recv(t int) int {
	if t > l.C {
		l.C = t
	}
	l.C++
	return l.C
}

// Vector adalah vector clock: satu penghitung untuk setiap proses.
type Vector []int

// NewVector membuat vektor nol berukuran n.
func NewVector(n int) Vector { return make(Vector, n) }

// Copy menyalin vektor agar event yang tersimpan tidak ikut berubah.
func (v Vector) Copy() Vector { c := make(Vector, len(v)); copy(c, v); return c }

// Tick menaikkan elemen milik proses i (kejadian lokal atau pengiriman).
func (v Vector) Tick(i int) { v[i]++ }

// Merge dipakai saat menerima: ambil maksimum per elemen, lalu naikkan elemen ke-i.
func (v Vector) Merge(in Vector, i int) {
	for k := range v {
		if k < len(in) && in[k] > v[k] {
			v[k] = in[k]
		}
	}
	v[i]++
}

// Order adalah hasil perbandingan dua vector clock.
type Order string

const (
	Equal      Order = "sama"
	Before     Order = "a sebelum b"
	After      Order = "b sebelum a"
	Concurrent Order = "concurrent"
)

// Compare membandingkan a dan b per elemen.
func Compare(a, b Vector) Order {
	le, ge := true, true
	for k := range a {
		if a[k] > b[k] {
			le = false
		}
		if a[k] < b[k] {
			ge = false
		}
	}
	switch {
	case le && ge:
		return Equal
	case le:
		return Before
	case ge:
		return After
	default:
		return Concurrent
	}
}
