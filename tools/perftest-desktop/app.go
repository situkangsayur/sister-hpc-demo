package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/engine"
	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/presets"
)

// App adalah objek yang di-bind ke frontend (window.go.main.App).
// Semua perhitungan ada di package engine; App hanya mengatur siklus run dan dialog file.
type App struct {
	ctx context.Context

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	last    *engine.Report
	initial *Initial
}

// Initial adalah skenario dari argumen --config (dan --autostart) saat aplikasi dibuka.
type Initial struct {
	Config    engine.Config `json:"config"`
	Autostart bool          `json:"autostart"`
}

// InitialConfig mengembalikan skenario awal dari argumen, atau nil.
func (a *App) InitialConfig() *Initial { return a.initial }

// NewApp membuat App.
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// Version mengembalikan versi aplikasi.
func (a *App) Version() string { return version }

// DefaultHost adalah nilai awal kolom Host: env BIND_ADDR atau 127.0.0.1.
func (a *App) DefaultHost() string {
	if v := os.Getenv("BIND_ADDR"); v != "" {
		return v
	}
	return engine.DefaultBindAddr
}

// Presets mengembalikan preset bawaan.
func (a *App) Presets() []presets.Preset { return presets.Builtin() }

// LoadPresetFile membuka dialog dan memuat file preset YAML.
func (a *App) LoadPresetFile() ([]presets.Preset, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Muat preset YAML",
		Filters: []runtime.FileFilter{{DisplayName: "YAML", Pattern: "*.yaml;*.yml"}},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return presets.LoadFile(path)
}

// GRPCMethods mendaftar method unary lewat server reflection.
func (a *App) GRPCMethods(target string, useTLS, insecure bool, host string) ([]engine.GRPCMethodInfo, error) {
	c := engine.Config{Target: target}.Expand(map[string]string{"BIND_ADDR": host})
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return engine.ListGRPCMethods(ctx, c.Target, useTLS, insecure)
}

// Start memulai run di background. Event ke frontend: "tick" (Point per detik),
// "hist" (histogram kumulatif), "done" (Report), "failed" (pesan error).
func (a *App) Start(cfg engine.Config, host string) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("run lain masih berjalan")
	}
	vars := map[string]string{"BIND_ADDR": host}
	check := cfg.Expand(vars)
	if _, err := check.Validate(); err != nil {
		a.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancel, a.running = cancel, true
	a.mu.Unlock()

	go func() {
		defer cancel()
		rep, err := engine.Run(ctx, cfg, engine.Options{
			Vars:        vars,
			OnTick:      func(p engine.Point) { runtime.EventsEmit(a.ctx, "tick", p) },
			OnHistogram: func(b []engine.Bin) { runtime.EventsEmit(a.ctx, "hist", b) },
		})
		a.mu.Lock()
		a.running, a.cancel = false, nil
		if err == nil {
			a.last = &rep
		}
		a.mu.Unlock()
		if err != nil {
			runtime.EventsEmit(a.ctx, "failed", err.Error())
			return
		}
		runtime.EventsEmit(a.ctx, "done", rep)
	}()
	return nil
}

// Stop menghentikan run yang sedang berjalan (hasil sejauh ini tetap dilaporkan).
func (a *App) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

// Export menyimpan hasil run terakhir sebagai "csv" atau "json" lewat dialog simpan.
// Mengembalikan path file, atau "" bila dibatalkan.
func (a *App) Export(format string) (string, error) {
	a.mu.Lock()
	rep := a.last
	a.mu.Unlock()
	if rep == nil {
		return "", errors.New("belum ada hasil run")
	}
	var write func(io.Writer) error
	switch format {
	case "csv":
		write = rep.WriteCSV
	case "json":
		write = rep.WriteJSON
	default:
		return "", fmt.Errorf("format tidak dikenal %q", format)
	}
	name := fmt.Sprintf("perftest-%s.%s", rep.StartedAt.Format("20060102-150405"), format)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Simpan hasil perftest",
		DefaultFilename: name,
	})
	if err != nil || path == "" {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}
