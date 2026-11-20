package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Breaker harus terbuka setelah panggilan gagal, dan view() boleh dipanggil bersamaan
// dengan transisi state tanpa deadlock.
func TestBreakerOpensAndViewDoesNotDeadlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := newRemote("uji", srv.URL, 200*time.Millisecond, 1, nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.view()
			}
		}
	}()
	for i := 0; i < 6; i++ {
		_, _, _ = r.get(context.Background(), "/x")
	}
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock antara view() dan OnStateChange")
	}
	if st := r.view().State; st != "open" {
		t.Fatalf("breaker seharusnya open, dapat %s", st)
	}
}
