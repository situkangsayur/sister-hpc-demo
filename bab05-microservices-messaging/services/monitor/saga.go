package main

import (
	"sort"
	"strings"
	"sync"
	"time"

	"sister-bab05/services/platform"
)

// Event adalah satu baris timeline: pesan yang diterbitkan (publish) atau catatan
// consumer (audit), dilihat monitor dengan berlangganan shop.>, audit.>, dan lab.>.
type Event struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"` // publish | audit | lab
	Subject   string    `json:"subject"`
	From      string    `json:"from"`
	To        []string  `json:"to,omitempty"`
	MsgID     string    `json:"msg_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	OrderID   string    `json:"order_id,omitempty"`
	Action    string    `json:"action,omitempty"`
	Note      string    `json:"note,omitempty"`
	Delivered int       `json:"delivered,omitempty"`
	Replica   string    `json:"replica,omitempty"`
}

// consumersOf memetakan subject ke service yang berlangganan (untuk panah timeline).
func consumersOf(subject string) []string {
	switch subject {
	case platform.SubjOrderCreated:
		return []string{"inventory"}
	case platform.SubjInventoryReserved:
		return []string{"payment"}
	case platform.SubjInventoryRejected, platform.SubjPaymentSucceeded, platform.SubjInventoryReleased:
		return []string{"order"}
	case platform.SubjPaymentFailed:
		return []string{"inventory"}
	case platform.SubjOrderConfirmed:
		return []string{"notification", "inventory"}
	case platform.SubjOrderCancelled:
		return []string{"notification"}
	}
	return nil
}

// publisherOf: shop.<service>.<event> diterbitkan oleh <service>.
func publisherOf(subject string) string {
	p := strings.Split(subject, ".")
	if len(p) >= 2 && p[0] == "shop" {
		return p[1]
	}
	return "monitor"
}

// Langkah saga yang ditampilkan UI, urut sesuai Gambar saga pemesanan di Bab 5.
var stepNames = []string{"T1", "T2", "T3", "T4", "C2", "C1", "N"}

// SagaView adalah status saga satu order hasil rekonstruksi dari event.
type SagaView struct {
	OrderID      string            `json:"order_id"`
	RequestID    string            `json:"request_id"`
	Steps        map[string]string `json:"steps"` // nama langkah -> ok | failed | pending
	Outcome      string            `json:"outcome"`
	Duplicates   int               `json:"duplicates"`
	Redeliveries int               `json:"redeliveries"`
	FirstSeen    time.Time         `json:"first_seen"`
	LastSeen     time.Time         `json:"last_seen"`
}

// applyEvent memperbarui status saga dari satu event. Fungsi murni, diuji di saga_test.go.
func applyEvent(s *SagaView, e Event) {
	set := func(step, v string) { s.Steps[step] = v }
	if e.Kind == "publish" {
		switch e.Subject {
		case platform.SubjOrderCreated:
			set("T1", "ok")
		case platform.SubjInventoryReserved:
			set("T2", "ok")
		case platform.SubjInventoryRejected:
			set("T2", "failed")
		case platform.SubjPaymentSucceeded:
			set("T3", "ok")
		case platform.SubjPaymentFailed:
			set("T3", "failed")
		case platform.SubjInventoryReleased:
			set("C2", "ok")
		case platform.SubjOrderConfirmed:
			set("T4", "ok")
		case platform.SubjOrderCancelled:
			set("C1", "ok")
		}
	}
	if e.Kind == "audit" {
		switch e.Action {
		case "duplicate":
			s.Duplicates++
		case "redelivered":
			s.Redeliveries++
		case "consumed":
			if e.From == "notification" {
				set("N", "ok")
			}
		}
	}
	if s.RequestID == "" {
		s.RequestID = e.RequestID
	}
	switch {
	case s.Steps["T4"] == "ok":
		s.Outcome = "CONFIRMED"
	case s.Steps["C1"] == "ok" && s.Steps["C2"] == "ok":
		s.Outcome = "CANCELLED (dikompensasi)"
	case s.Steps["C1"] == "ok":
		s.Outcome = "CANCELLED"
	case s.Steps["T3"] == "failed":
		s.Outcome = "KOMPENSASI BERJALAN"
	default:
		s.Outcome = "BERJALAN"
	}
}

// store menyimpan 1500 event terakhir dan status saga 40 order terakhir di memori.
type store struct {
	mu     sync.Mutex
	seq    int64
	events []Event
	sagas  map[string]*SagaView
}

func newStore() *store { return &store{sagas: map[string]*SagaView{}} }

func (s *store) add(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.Seq = s.seq
	s.events = append(s.events, e)
	if len(s.events) > 1500 {
		s.events = s.events[len(s.events)-1500:]
	}
	if e.OrderID == "" {
		return
	}
	sv, ok := s.sagas[e.OrderID]
	if !ok {
		sv = &SagaView{OrderID: e.OrderID, Steps: map[string]string{}, FirstSeen: e.At}
		for _, n := range stepNames {
			sv.Steps[n] = "pending"
		}
		s.sagas[e.OrderID] = sv
		if len(s.sagas) > 40 {
			var oldest string
			for id, v := range s.sagas {
				if oldest == "" || v.FirstSeen.Before(s.sagas[oldest].FirstSeen) {
					oldest = id
				}
			}
			delete(s.sagas, oldest)
		}
	}
	sv.LastSeen = e.At
	applyEvent(sv, e)
}

func (s *store) since(seq int64, limit int) ([]Event, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for _, e := range s.events {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, s.seq
}

func (s *store) sagaList() []SagaView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SagaView, 0, len(s.sagas))
	for _, v := range s.sagas {
		c := *v
		c.Steps = map[string]string{}
		for k, x := range v.Steps {
			c.Steps[k] = x
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FirstSeen.After(out[j].FirstSeen) })
	return out
}
