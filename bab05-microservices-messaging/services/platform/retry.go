package platform

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// ErrPermanent menandai error yang tidak boleh di-retry (mis. breaker open, 4xx).
var ErrPermanent = errors.New("permanent")

// Backoff menghitung jeda percobaan ke-k (mulai 0) dengan full jitter:
// acak(0, min(batas, dasar*2^k)). Rumus ini dibahas di Bab 5 (Timeout, retry, backoff, jitter).
func Backoff(k int, base, maxDelay time.Duration, rnd func() float64) time.Duration {
	ceil := base << k
	if ceil > maxDelay || ceil <= 0 {
		ceil = maxDelay
	}
	return time.Duration(rnd() * float64(ceil))
}

// Retry menjalankan fn sampai berhasil, paling banyak attempts kali. fn wajib idempotent.
// Error yang membungkus ErrPermanent langsung dikembalikan tanpa retry.
func Retry(ctx context.Context, attempts int, base, maxDelay time.Duration, fn func(attempt int) error) error {
	var err error
	for k := 0; k < attempts; k++ {
		if err = fn(k); err == nil || errors.Is(err, ErrPermanent) {
			return err
		}
		if k == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(Backoff(k, base, maxDelay, rand.Float64)):
		}
	}
	return err
}
