// Command perftest adalah mode CLI (tanpa layar) dari perftest-desktop: cocok untuk uji
// otomatis dan server tanpa desktop. Dibangun murni Go (CGO_ENABLED=0).
package main

import (
	"os"

	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
