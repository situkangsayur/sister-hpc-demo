// Service chat: ruang chat di atas socket TCP mentah (Bab 2, socket API).
//
// Protokolnya satu baris = satu pesan (framing dengan '\n'), sehingga dapat
// dicoba langsung dengan `nc host 9100`. Browser ikut lewat service chatgw
// yang menerjemahkan WebSocket <-> baris TCP.
package main

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/logx"
)

func main() {
	log := logx.New("chat")
	room := NewRoom()

	ln, err := net.Listen("tcp", ":9100") // socket() + bind() + listen()
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
	go func() {
		for {
			conn, err := ln.Accept() // satu socket baru per client
			if err != nil {
				continue
			}
			go serve(room, conn, log)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/clients", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, room.Clients())
	})
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, room.History())
	})
	log.Info("chat siap", "tcp", 9100, "http", 8090)
	srv := &http.Server{Addr: ":8090", Handler: httpx.Logging(log, mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http berhenti", "err", err)
		os.Exit(1)
	}
}

type logger interface {
	Info(msg string, args ...any)
}

func serve(room *Room, conn net.Conn, log logger) {
	remote := conn.RemoteAddr().String()
	c := room.Join(remote)
	log.Info("client terhubung", "id", c.ID, "remote", remote)
	defer func() {
		room.Leave(c)
		_ = conn.Close()
		log.Info("client terputus", "id", c.ID, "nick", c.Nick, "remote", remote)
	}()

	// goroutine penulis: satu-satunya yang menulis ke socket ini
	go func() {
		w := bufio.NewWriter(conn)
		for {
			select {
			case line := <-c.out:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := w.WriteString(line + "\n"); err != nil {
					c.close()
					return
				}
				if len(c.out) == 0 {
					if err := w.Flush(); err != nil {
						c.close()
						return
					}
				}
			case <-c.quit:
				_ = w.Flush()
				_ = conn.Close() // membangunkan pembaca di bawah
				return
			}
		}
	}()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 4096), 4096) // batas panjang baris: lindungi memori server
	for sc.Scan() {
		if !room.Handle(c, sc.Text()) {
			time.Sleep(50 * time.Millisecond) // beri kesempatan "sampai jumpa" terkirim
			return
		}
	}
}
