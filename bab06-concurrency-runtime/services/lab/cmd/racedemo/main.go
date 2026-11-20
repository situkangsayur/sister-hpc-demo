// Command racedemo adalah program kecil yang sengaja dibangun dengan `go build -race`.
// Mode "unsafe" memicu laporan "WARNING: DATA RACE"; mode lain bersih menurut detektor.
package main

import (
	"flag"
	"fmt"
	"sync"
	"sync/atomic"
)

var saldo int64 // data bersama

func main() {
	mode := flag.String("mode", "unsafe", "unsafe | mutex | atomic | channel")
	g := flag.Int("g", 4, "jumlah goroutine")
	n := flag.Int("n", 2000, "tambah per goroutine")
	flag.Parse()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var at atomic.Int64
	tambah := make(chan int64, 64)
	selesai := make(chan int64)
	if *mode == "channel" {
		go func() {
			var s int64
			for d := range tambah {
				s += d
			}
			selesai <- s
		}()
	}

	for t := 0; t < *g; t++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < *n; i++ {
				switch *mode {
				case "unsafe":
					saldo++ // data race: baca-tambah-tulis tanpa sinkronisasi
				case "mutex":
					mu.Lock()
					saldo++
					mu.Unlock()
				case "atomic":
					at.Add(1)
				case "channel":
					tambah <- 1
				}
			}
		}()
	}
	wg.Wait()

	var hasil int64
	switch *mode {
	case "atomic":
		hasil = at.Load()
	case "channel":
		close(tambah)
		hasil = <-selesai
	default:
		hasil = saldo
	}
	fmt.Printf("mode=%s hasil=%d seharusnya=%d\n", *mode, hasil, int64(*g)*int64(*n))
}
