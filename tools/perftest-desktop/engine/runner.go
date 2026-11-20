package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Options mengatur perilaku Run di luar Config.
type Options struct {
	// TickInterval adalah lebar interval grafik live (default 1 detik).
	TickInterval time.Duration
	// OnTick dipanggil setiap interval dengan ringkasan interval itu (boleh nil).
	OnTick func(Point)
	// OnHistogram dipanggil setiap interval dengan histogram kumulatif 30 batang (boleh nil).
	OnHistogram func([]Bin)
	// Vars untuk ekspansi ${BIND_ADDR} dsb. (boleh nil; lihat Config.Expand).
	Vars map[string]string
}

// Run menjalankan satu skenario sampai durasi habis, jumlah request tercapai, atau ctx dibatalkan
// (tombol Stop). Model beban: Concurrency goroutine worker tertutup (closed model); bila RPS > 0,
// sebuah pacer membagikan "token" dengan laju RPS dan worker hanya mengirim bila mendapat token.
// Token yang tidak terambil karena semua worker sibuk dibuang (tidak menumpuk), sehingga RPS
// tercapai hanya bila concurrency cukup.
func Run(ctx context.Context, cfg Config, opt Options) (Report, error) {
	cfg = cfg.Expand(opt.Vars)
	lim, err := cfg.Validate()
	if err != nil {
		return Report{Config: cfg}, err
	}
	if opt.TickInterval <= 0 {
		opt.TickInterval = time.Second
	}

	target, err := NewTarget(ctx, cfg)
	if err != nil {
		return Report{Config: cfg}, err
	}
	defer target.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if lim.Duration > 0 {
		var c2 context.CancelFunc
		runCtx, c2 = context.WithTimeout(runCtx, lim.Duration)
		defer c2()
	}

	start := time.Now()
	rec := NewRecorder(start)

	var tokens chan struct{}
	if cfg.RPS > 0 {
		tokens = make(chan struct{}, cfg.Concurrency)
		go pace(runCtx, cfg.RPS, tokens)
	}

	var issued atomic.Int64
	var wg sync.WaitGroup
	for range cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := target.NewWorker()
			defer w.Close()
			for {
				if runCtx.Err() != nil {
					return
				}
				if tokens != nil {
					select {
					case <-runCtx.Done():
						return
					case <-tokens:
					}
				}
				if lim.Requests > 0 && issued.Add(1) > int64(lim.Requests) {
					return
				}
				reqCtx, rc := context.WithTimeout(runCtx, lim.Timeout)
				t0 := time.Now()
				err := w.Do(reqCtx)
				lat := time.Since(t0)
				rc()
				if err != nil && runCtx.Err() != nil {
					// request terpotong karena run berakhir/dihentikan: tidak dihitung
					return
				}
				rec.Add(Sample{Latency: lat, Err: Classify(err)})
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	tk := time.NewTicker(opt.TickInterval)
	defer tk.Stop()
loop:
	for {
		select {
		case <-done:
			break loop
		case now := <-tk.C:
			p := rec.Tick(now)
			if opt.OnTick != nil {
				opt.OnTick(p)
			}
			if opt.OnHistogram != nil {
				opt.OnHistogram(rec.Bins(30))
			}
		}
	}
	end := time.Now()
	// interval terakhir yang belum penuh
	if p := rec.Tick(end); opt.OnTick != nil && (p.OK+p.Errors) > 0 {
		opt.OnTick(p)
	}
	rep := rec.Report(cfg, end)
	rep.Stopped = ctx.Err() != nil
	return rep, nil
}

// pace mengisi channel token dengan laju rps memakai token bucket ber-resolusi 5 ms
// (lebih stabil daripada time.Ticker untuk laju tinggi).
func pace(ctx context.Context, rps float64, tokens chan<- struct{}) {
	const step = 5 * time.Millisecond
	tk := time.NewTicker(step)
	defer tk.Stop()
	last := time.Now()
	acc := 1.0 // token pertama langsung
	for {
		for acc >= 1 {
			select {
			case tokens <- struct{}{}:
			default: // semua worker sibuk: buang token
			}
			acc--
		}
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			acc += rps * now.Sub(last).Seconds()
			last = now
		}
	}
}
