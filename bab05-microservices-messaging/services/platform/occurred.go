package platform

import (
	"errors"
	"time"
)

// MaxFutureSkew adalah toleransi jam perangkat: occurred_at boleh paling jauh 5 menit di depan.
const MaxFutureSkew = 5 * time.Minute

// Pesan kesalahan occurred_at (dipakai bank dan order agar aturannya sama persis).
var (
	ErrOccurredFormat = errors.New("occurred_at harus RFC3339, mis. 2026-11-20T08:15:00+07:00")
	ErrOccurredFuture = errors.New("occurred_at tidak boleh di masa depan")
)

// ParseOccurred membaca occurred_at (waktu bisnis) dari klien. Kosong = now. Waktu lampau
// diterima (transaksi offline, backfill); lebih dari 5 menit di masa depan ditolak.
func ParseOccurred(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, ErrOccurredFormat
	}
	if t.After(now.Add(MaxFutureSkew)) {
		return time.Time{}, ErrOccurredFuture
	}
	return t, nil
}
