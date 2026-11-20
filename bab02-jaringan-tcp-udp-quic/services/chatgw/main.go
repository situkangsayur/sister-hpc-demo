// Service chatgw: gateway WebSocket ke ruang chat TCP.
//
// Browser tidak dapat membuka socket TCP mentah, jadi untuk setiap koneksi
// WebSocket gateway membuka SATU socket TCP ke service chat dan meneruskan
// baris di kedua arah. Di mata server chat, browser hanyalah client TCP lain.
package main

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/httpx"
	"github.com/situkangsayur/sister-hpc-demo/bab02/internal/logx"
)

var reNick = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,20}$`)

func main() {
	log := logx.New("chatgw")
	chatAddr := os.Getenv("CHAT_ADDR")
	if chatAddr == "" {
		chatAddr = "chat:9100"
	}
	up := websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		// demo kelas: UI dibuka dari host/port mana pun di jaringan lab
		CheckOrigin: func(*http.Request) bool { return true },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		nick := r.URL.Query().Get("nick")
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		tcp, err := net.DialTimeout("tcp", chatAddr, 5*time.Second)
		if err != nil {
			_ = ws.WriteMessage(websocket.TextMessage, []byte("*** gateway gagal terhubung ke server chat: "+err.Error()))
			return
		}
		defer tcp.Close()
		browser := r.RemoteAddr
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			browser = strings.TrimSpace(strings.Split(xf, ",")[0])
		}
		log.Info("websocket terhubung", "browser", browser, "tcp_local", tcp.LocalAddr().String(), "nick", nick)
		_, _ = tcp.Write([]byte("/via websocket " + strings.ReplaceAll(browser, " ", "") + "\n"))
		if reNick.MatchString(nick) {
			_, _ = tcp.Write([]byte("/nick " + nick + "\n"))
		}
		ws.SetReadLimit(4096)

		// TCP -> WebSocket: satu baris = satu frame teks
		done := make(chan struct{})
		go func() {
			defer close(done)
			sc := bufio.NewScanner(tcp)
			for sc.Scan() {
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ws.WriteMessage(websocket.TextMessage, sc.Bytes()); err != nil {
					return
				}
			}
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server chat menutup koneksi"), time.Now().Add(time.Second))
		}()
		// WebSocket -> TCP: frame teks menjadi baris (newline di dalam pesan diganti spasi)
		go func() {
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					_ = tcp.Close()
					return
				}
				line := strings.NewReplacer("\r", " ", "\n", " ").Replace(string(msg))
				if _, err := tcp.Write([]byte(line + "\n")); err != nil {
					return
				}
			}
		}()
		<-done
		log.Info("websocket terputus", "browser", browser, "nick", nick)
	})
	log.Info("chatgw siap", "http", 8091, "chat", chatAddr)
	srv := &http.Server{Addr: ":8091", Handler: httpx.Logging(log, mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("http berhenti", "err", err)
		os.Exit(1)
	}
}
