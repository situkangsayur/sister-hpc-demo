// Command perftest-desktop adalah aplikasi desktop (Wails v2: backend Go + frontend Next.js
// static export) untuk perftest semua demo Sistem Terdistribusi. Dengan argumen
// "run", "presets", atau "grpc-list", binary yang sama berjalan sebagai CLI tanpa layar.
package main

import (
	"embed"
	"flag"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/engine"
	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/internal/cli"
)

const version = "1.0.0"

// Hasil "npm run build" (Next.js output: export) di frontend/out. File .gitkeep menjaga
// folder tetap ada supaya go vet/build tidak gagal sebelum frontend dibangun.
//
//go:embed all:frontend/out
var assets embed.FS

func main() {
	if cli.IsCLI(os.Args[1:]) {
		os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
	}
	// Opsi desktop: --config skenario.yaml mengisi form saat dibuka; --autostart langsung mulai.
	fs := flag.NewFlagSet("perftest-desktop", flag.ExitOnError)
	cfgPath := fs.String("config", "", "skenario YAML yang dimuat ke form saat aplikasi dibuka")
	autostart := fs.Bool("autostart", false, "langsung mulai run skenario --config")
	_ = fs.Parse(os.Args[1:])
	app := NewApp()
	if *cfgPath != "" {
		c, err := engine.LoadConfig(*cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		app.initial = &Initial{Config: c, Autostart: *autostart}
	}
	err := wails.Run(&options.App{
		Title:            "Perftest · Sistem Terdistribusi",
		Width:            1400,
		Height:           900,
		MinWidth:         900,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 244, G: 245, B: 247, A: 255},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
