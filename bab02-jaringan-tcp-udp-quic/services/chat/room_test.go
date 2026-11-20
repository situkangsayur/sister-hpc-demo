package main

import (
	"strings"
	"testing"
	"time"
)

func drain(c *Client) []string {
	var out []string
	for {
		select {
		case l := <-c.out:
			out = append(out, l)
		default:
			return out
		}
	}
}

func TestRoomBroadcastAndNick(t *testing.T) {
	r := NewRoom()
	r.now = func() time.Time { return time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC) }
	a := r.Join("10.0.0.1:1111")
	b := r.Join("10.0.0.2:2222")
	r.Handle(b, "/via websocket 192.168.1.5")
	r.Handle(a, "/nick budi")
	drain(a)
	drain(b)
	r.Handle(a, "halo semua\r\n")
	got := drain(b)
	if len(got) != 1 || got[0] != "[10:00:00] budi: halo semua" {
		t.Fatalf("b menerima %q", got)
	}
	cl := r.Clients()
	if len(cl) != 2 || cl[0].Nick != "budi" || cl[1].Kind != "websocket" || cl[1].Origin != "192.168.1.5" {
		t.Fatalf("clients %+v", cl)
	}
	// nick ganda ditolak
	r.Handle(b, "/nick BUDI")
	if l := drain(b); len(l) != 1 || !strings.Contains(l[0], "sudah dipakai") {
		t.Fatalf("nick ganda: %q", l)
	}
	if r.Handle(a, "/quit") {
		t.Fatal("/quit harus mengembalikan false")
	}
	r.Leave(a)
	if len(r.Clients()) != 1 {
		t.Fatal("a harus sudah keluar")
	}
}

func TestSlowClientDropped(t *testing.T) {
	r := NewRoom()
	a := r.Join("x:1")
	for i := 0; i < 200; i++ {
		a.send("spam")
	}
	select {
	case <-a.quit:
	default:
		t.Fatal("client lambat harus diputus")
	}
}
