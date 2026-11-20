// Package logx menyiapkan logger JSON satu baris (log/slog) sesuai KONVENSI repo.
package logx

import (
	"log/slog"
	"os"
)

// New membuat logger JSON ke stdout dengan field service tetap.
func New(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := slog.New(h).With("service", service)
	slog.SetDefault(l)
	return l
}
