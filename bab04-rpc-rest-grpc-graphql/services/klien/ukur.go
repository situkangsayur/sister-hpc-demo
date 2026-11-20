package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// Statistik latency dalam milidetik. P50 dan P99 dipakai karena rata-rata
// menipu (Subbab Mengapa rata-rata menipu).
type Statistik struct {
	N      int       `json:"n"`
	Min    float64   `json:"min"`
	P50    float64   `json:"p50"`
	P90    float64   `json:"p90"`
	P99    float64   `json:"p99"`
	Maks   float64   `json:"maks"`
	Rata   float64   `json:"rata"`
	Sampel []float64 `json:"sampel"`
}

// persentil memakai metode nearest-rank pada data terurut.
func persentil(urut []float64, p float64) float64 {
	if len(urut) == 0 {
		return 0
	}
	k := int(math.Ceil(p/100*float64(len(urut)))) - 1
	if k < 0 {
		k = 0
	}
	if k >= len(urut) {
		k = len(urut) - 1
	}
	return urut[k]
}

func hitungStatistik(ms []float64) Statistik {
	if len(ms) == 0 {
		return Statistik{Sampel: []float64{}}
	}
	urut := slices.Clone(ms)
	slices.Sort(urut)
	jml := 0.0
	for _, v := range ms {
		jml += v
	}
	bulat := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	return Statistik{
		N: len(ms), Min: bulat(urut[0]), P50: bulat(persentil(urut, 50)), P90: bulat(persentil(urut, 90)),
		P99: bulat(persentil(urut, 99)), Maks: bulat(urut[len(urut)-1]), Rata: bulat(jml / float64(len(ms))), Sampel: ms,
	}
}

// hexSpasi menulis byte sebagai "08 2A 12 ...".
func hexSpasi(b []byte) string {
	s := strings.ToUpper(hex.EncodeToString(b))
	var sb strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(s[i : i+2])
	}
	return sb.String()
}

// AnotasiBaris adalah satu field Protobuf hasil bongkar wire format.
type AnotasiBaris struct {
	Hex        string `json:"hex"`
	Keterangan string `json:"keterangan"`
	Kedalaman  int    `json:"kedalaman"`
}

func cetakan(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r < 0x20 {
			return false
		}
	}
	return true
}

// anotasiProto membongkar wire format Protobuf tanpa skema: nomor field,
// wire type, dan nilainya (seperti tabel di slide P04). Untuk length-delimited,
// teks tercetak dianggap string; selain itu dicoba sebagai pesan bersarang.
func anotasiProto(b []byte, dalam int, batas *int) []AnotasiBaris {
	var hasil []AnotasiBaris
	for len(b) > 0 && *batas > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return append(hasil, AnotasiBaris{hexSpasi(b), "byte tidak dikenali", dalam})
		}
		tag := b[:n]
		b = b[n:]
		*batas--
		switch typ {
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return hasil
			}
			hasil = append(hasil, AnotasiBaris{hexSpasi(append(slices.Clone(tag), b[:m]...)),
				fmt.Sprintf("field %d, varint = %d", num, v), dalam})
			b = b[m:]
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return hasil
			}
			kepala := append(slices.Clone(tag), b[:m-len(v)]...)
			if cetakan(v) {
				hasil = append(hasil, AnotasiBaris{hexSpasi(append(kepala, v...)),
					fmt.Sprintf("field %d, panjang %d, \"%s\"", num, len(v), string(v)), dalam})
			} else {
				hasil = append(hasil, AnotasiBaris{hexSpasi(kepala),
					fmt.Sprintf("field %d, panjang %d, pesan bersarang:", num, len(v)), dalam})
				hasil = append(hasil, anotasiProto(v, dalam+1, batas)...)
			}
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return hasil
			}
			hasil = append(hasil, AnotasiBaris{hexSpasi(append(slices.Clone(tag), b[:m]...)),
				fmt.Sprintf("field %d, wire type %d", num, typ), dalam})
			b = b[m:]
		}
	}
	if len(b) > 0 {
		hasil = append(hasil, AnotasiBaris{"…", fmt.Sprintf("%d byte lagi tidak ditampilkan", len(b)), dalam})
	}
	return hasil
}
