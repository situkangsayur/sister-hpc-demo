package main

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"sister-bab05/services/platform"
)

// Nilai enum mengikuti kontrak data DE (de-demo/platform/KONTRAK-DATA.md). Jangan diubah
// tanpa memberi tahu koordinator: pipeline silver/gold membaca nilai yang sama.
var (
	JenisRekening  = []string{"tabungan_wadiah", "giro", "deposito_mudharabah"}
	Kanal          = []string{"mobile", "atm", "teller", "web", "ecommerce"}
	JenisTransaksi = []string{"setor", "tarik", "transfer_masuk", "transfer_keluar", "bayar_belanja", "angsuran"}
	StatusAkad     = []string{"diajukan", "disetujui", "cair", "lunas", "ditolak"}
	Segmen         = []string{"ritel", "prioritas", "umkm"}
	// KodeAkadValid hanya informasi untuk UI; kode lain tetap disimpan apa adanya
	// (data kotor yang sengaja dibiarkan untuk dikarantina di lapisan silver DE).
	KodeAkadValid = map[string]string{"MRB": "murabahah", "MSY": "musyarakah", "MDH": "mudharabah", "IJR": "ijarah"}
)

// apiError membawa kode HTTP dan pesan untuk klien.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return e.Msg }

func bad(code int, format string, a ...any) error {
	return &apiError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// ErrSaldoKurang dikembalikan bila debit membuat saldo negatif.
var ErrSaldoKurang = errors.New("saldo tidak cukup")

// isDebit: jenis transaksi yang mengurangi saldo.
func isDebit(jenis string) bool {
	switch jenis {
	case "tarik", "transfer_keluar", "bayar_belanja", "angsuran":
		return true
	}
	return false
}

// applySaldo menghitung saldo baru setelah satu transaksi (fungsi murni, diuji).
func applySaldo(saldo int64, jenis string, nominal int64) (int64, error) {
	if !contains(JenisTransaksi, jenis) {
		return saldo, bad(400, "jenis transaksi %q tidak dikenal", jenis)
	}
	if nominal <= 0 || nominal > 1_000_000_000_000 {
		return saldo, bad(400, "nominal harus 1..1.000.000.000.000")
	}
	if isDebit(jenis) {
		if saldo < nominal {
			return saldo, ErrSaldoKurang
		}
		return saldo - nominal, nil
	}
	return saldo + nominal, nil
}

// parseOccurred membaca occurred_at dari klien. Kosong = sekarang. Waktu lampau boleh
// (transaksi offline perangkat yang baru tersinkron = data terlambat); masa depan > 5 menit ditolak.
func parseOccurred(s string, now time.Time) (time.Time, error) {
	t, err := platform.ParseOccurred(s, now)
	if err != nil {
		return time.Time{}, bad(400, "%s", err.Error())
	}
	return t, nil
}

// nextAkad adalah mesin status akad pembiayaan:
//
//	diajukan --setujui--> disetujui --cairkan--> cair --angsuran (lunas)--> lunas
//	diajukan|disetujui --tolak--> ditolak
func nextAkad(current, aksi string) (string, error) {
	switch {
	case aksi == "setujui" && current == "diajukan":
		return "disetujui", nil
	case aksi == "tolak" && (current == "diajukan" || current == "disetujui"):
		return "ditolak", nil
	case aksi == "cairkan" && current == "disetujui":
		return "cair", nil
	case aksi == "angsuran" && current == "cair":
		return "cair", nil // menjadi lunas bila terbayar >= pokok + margin (lihat angsuranAkad)
	}
	return "", bad(409, "aksi %q tidak berlaku untuk akad berstatus %q", aksi, current)
}

// angsuranAkad menghitung nominal angsuran (default: (pokok+margin)/tenor dibulatkan ke atas,
// tidak melebihi sisa) dan apakah akad menjadi lunas.
func angsuranAkad(pokok, margin int64, tenor int, terbayar, diminta int64) (nominal int64, lunas bool) {
	total := pokok + margin
	sisa := total - terbayar
	if sisa <= 0 {
		return 0, true
	}
	nominal = diminta
	if nominal <= 0 {
		t := int64(max(tenor, 1))
		nominal = (total + t - 1) / t
	}
	nominal = min(nominal, sisa)
	return nominal, terbayar+nominal >= total
}

func contains(list []string, v string) bool { return slices.Contains(list, v) }

// prefixRekening: digit pertama nomor rekening menandai jenisnya.
func prefixRekening(jenis string) string {
	switch jenis {
	case "giro":
		return "2"
	case "deposito_mudharabah":
		return "3"
	}
	return "1"
}
