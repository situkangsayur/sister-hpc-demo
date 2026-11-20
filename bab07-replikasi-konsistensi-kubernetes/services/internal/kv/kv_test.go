package kv

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/raft"
)

func entry(t *testing.T, c Command) *raft.Log {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return &raft.Log{Data: b}
}

// Dua FSM yang menerapkan log yang sama dengan urutan sama harus berakhir di state yang sama.
func TestApplyDeterministik(t *testing.T) {
	log := []Command{{Op: "set", Key: "harga", Value: "10"}, {Op: "set", Key: "harga", Value: "12"},
		{Op: "set", Key: "stok", Value: "5"}, {Op: "delete", Key: "stok"}}
	a, b := New(), New()
	for _, c := range log {
		a.Apply(entry(t, c))
		b.Apply(entry(t, c))
	}
	if a.Data()["harga"] != "12" || len(a.Data()) != 1 {
		t.Fatalf("state a salah: %v", a.Data())
	}
	if a.Data()["harga"] != b.Data()["harga"] {
		t.Fatal("dua replica berbeda")
	}
}
