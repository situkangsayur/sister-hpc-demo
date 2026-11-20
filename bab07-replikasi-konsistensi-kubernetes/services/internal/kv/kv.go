// Package kv berisi state machine key-value yang direplikasi Raft.
package kv

import (
	"encoding/json"
	"io"
	"sort"
	"sync"

	"github.com/hashicorp/raft"
)

// Command adalah satu entri log: operasi set atau delete.
type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// FSM menerapkan entri log yang sudah commit ke map.
type FSM struct {
	mu   sync.RWMutex
	data map[string]string
}

// New membuat FSM kosong.
func New() *FSM { return &FSM{data: map[string]string{}} }

// Apply dipanggil Raft untuk setiap entri yang sudah commit, dengan urutan sama di semua node.
func (f *FSM) Apply(l *raft.Log) any {
	var c Command
	if err := json.Unmarshal(l.Data, &c); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch c.Op {
	case "set":
		f.data[c.Key] = c.Value
	case "delete":
		delete(f.data, c.Key)
	}
	return nil
}

// Snapshot menyalin seluruh map agar log lama dapat dipangkas.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	return &snap{data: f.Data()}, nil
}

// Restore mengganti state dari snapshot.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	m := map[string]string{}
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return err
	}
	f.mu.Lock()
	f.data = m
	f.mu.Unlock()
	return nil
}

// Data mengembalikan salinan map.
func (f *FSM) Data() map[string]string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c := make(map[string]string, len(f.data))
	for k, v := range f.data {
		c[k] = v
	}
	return c
}

// Keys mengembalikan kunci terurut (untuk tampilan).
func (f *FSM) Keys() []string {
	d := f.Data()
	ks := make([]string, 0, len(d))
	for k := range d {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

type snap struct{ data map[string]string }

func (s *snap) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s.data); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *snap) Release() {}
