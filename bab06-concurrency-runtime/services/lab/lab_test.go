package main

import (
	"context"
	"testing"
	"time"
)

// Mode aman harus selalu tepat G*N. Mode "unsafe" sengaja tidak diuji di sini karena
// hasilnya bergantung pada interleaving (dan akan ditandai bila test dijalankan dengan -race).
func TestCounterSafeModesExact(t *testing.T) {
	for _, m := range []string{"mutex", "atomic", "channel", "actor"} {
		if got := runCounter(m, 50, 2000); got != 100000 {
			t.Errorf("mode %s: dapat %d, seharusnya 100000", m, got)
		}
	}
}

func TestFindCycle(t *testing.T) {
	wf := map[string]string{"T1": "T2", "T2": "T1"}
	c := findCycle(wf, []string{"T1", "T2"})
	if len(c) != 2 {
		t.Fatalf("siklus 2 thread tidak terdeteksi: %v", c)
	}
	wf = map[string]string{"T1": "T2", "T2": "T3", "T3": "T1"}
	if c := findCycle(wf, []string{"T1", "T2", "T3"}); len(c) != 3 {
		t.Fatalf("siklus 3 thread tidak terdeteksi: %v", c)
	}
	wf = map[string]string{"T1": "T2"} // rantai tanpa siklus
	if c := findCycle(wf, []string{"T1", "T2"}); c != nil {
		t.Fatalf("seharusnya tidak ada siklus: %v", c)
	}
}

func TestLockMgrTimeout(t *testing.T) {
	m := newLockMgr([]string{"a"})
	ctx := context.Background()
	if err := m.acquire(ctx, "T1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.acquireTimeout(ctx, "T2", "a", 30*time.Millisecond); err != errTimeout {
		t.Fatalf("seharusnya timeout, dapat %v", err)
	}
	m.release("T1", "a")
	if err := m.acquireTimeout(ctx, "T2", "a", 30*time.Millisecond); err != nil {
		t.Fatalf("seharusnya dapat lock: %v", err)
	}
}

func TestPoolIOScalesWithWorkers(t *testing.T) {
	calibrateCPU()
	one := runPool("io", 1, 40, 5)
	many := runPool("io", 40, 40, 5)
	if many.Throughput < one.Throughput*5 {
		t.Fatalf("I/O-bound seharusnya jauh lebih cepat dengan banyak worker: 1=%.0f/s 40=%.0f/s",
			one.Throughput, many.Throughput)
	}
}

func TestMedianPct(t *testing.T) {
	if m := median([]float64{3, 1, 2}); m != 2 {
		t.Fatalf("median salah: %v", m)
	}
	if p := pct([]float64{1, 2, 3, 4, 5}, 0.5); p != 3 {
		t.Fatalf("pct salah: %v", p)
	}
}
