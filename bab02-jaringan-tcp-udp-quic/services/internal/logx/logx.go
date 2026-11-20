// Package logx membuat logger JSON satu baris (log/slog) sesuai KONVENSI repo.
package logx

import (
	"log/slog"
	"os"

	_ "time/tzdata" // jam lokal (TZ=Asia/Jakarta) tanpa paket tzdata di image
)

// New mengembalikan logger JSON ke stdout dengan field service tetap.
func New(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h).With("service", service)
}
