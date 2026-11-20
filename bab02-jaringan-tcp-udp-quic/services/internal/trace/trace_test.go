package trace

import "testing"

func TestRingEviction(t *testing.T) {
	r := NewRing(2)
	r.Add("a", Event{Call: "accept()"})
	r.Add("b", Event{Call: "accept()"})
	r.Add("a", Event{Call: "recv()"})
	r.Add("c", Event{Call: "accept()"})
	if len(r.Get("a")) != 0 {
		t.Fatal("a seharusnya sudah dibuang")
	}
	if len(r.Get("b")) != 1 || len(r.Get("c")) != 1 {
		t.Fatal("b dan c harus ada")
	}
}
