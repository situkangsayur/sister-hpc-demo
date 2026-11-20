package platform

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffFullJitterBounds(t *testing.T) {
	base, max := 100*time.Millisecond, 2*time.Second
	cases := []struct {
		k    int
		ceil time.Duration
	}{{0, 100 * time.Millisecond}, {1, 200 * time.Millisecond}, {3, 800 * time.Millisecond}, {5, 2 * time.Second}, {40, 2 * time.Second}}
	for _, c := range cases {
		if got := Backoff(c.k, base, max, func() float64 { return 0.999999 }); got > c.ceil || got < c.ceil*99/100 {
			t.Errorf("k=%d: jeda %v, batas atas seharusnya %v", c.k, got, c.ceil)
		}
		if got := Backoff(c.k, base, max, func() float64 { return 0 }); got != 0 {
			t.Errorf("k=%d: jeda minimum harus 0, dapat %v", c.k, got)
		}
	}
}

func TestRetryStopsOnPermanent(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), 5, time.Millisecond, time.Millisecond, func(int) error {
		calls++
		return errors.Join(ErrPermanent, errors.New("breaker open"))
	})
	if calls != 1 || !errors.Is(err, ErrPermanent) {
		t.Fatalf("harus berhenti setelah 1 panggilan, dapat %d (%v)", calls, err)
	}
}

func TestRetryEventuallySucceeds(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), 4, time.Millisecond, 2*time.Millisecond, func(int) error {
		calls++
		if calls < 3 {
			return errors.New("sementara")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("harus sukses di percobaan ke-3, dapat calls=%d err=%v", calls, err)
	}
}
