package main

import (
	"testing"
	"time"

	"sister-bab05/services/platform"
)

func feed(subjects ...string) SagaView {
	st := newStore()
	for _, s := range subjects {
		st.add(Event{At: time.Now(), Kind: "publish", Subject: s, OrderID: "ord-1"})
	}
	return st.sagaList()[0]
}

func TestSagaHappyPath(t *testing.T) {
	v := feed(platform.SubjOrderCreated, platform.SubjInventoryReserved, platform.SubjPaymentSucceeded, platform.SubjOrderConfirmed)
	if v.Outcome != "CONFIRMED" || v.Steps["C2"] != "pending" {
		t.Fatalf("dapat %+v", v)
	}
}

func TestSagaCompensation(t *testing.T) {
	v := feed(platform.SubjOrderCreated, platform.SubjInventoryReserved, platform.SubjPaymentFailed)
	if v.Outcome != "KOMPENSASI BERJALAN" {
		t.Fatalf("sebelum C2/C1: %s", v.Outcome)
	}
	v = feed(platform.SubjOrderCreated, platform.SubjInventoryReserved, platform.SubjPaymentFailed,
		platform.SubjInventoryReleased, platform.SubjOrderCancelled)
	if v.Outcome != "CANCELLED (dikompensasi)" || v.Steps["T3"] != "failed" || v.Steps["C2"] != "ok" {
		t.Fatalf("dapat %+v", v)
	}
}

func TestSagaRejectedNoCompensation(t *testing.T) {
	v := feed(platform.SubjOrderCreated, platform.SubjInventoryRejected, platform.SubjOrderCancelled)
	if v.Outcome != "CANCELLED" || v.Steps["T2"] != "failed" || v.Steps["C2"] != "pending" {
		t.Fatalf("dapat %+v", v)
	}
}

func TestDuplicateCounted(t *testing.T) {
	st := newStore()
	st.add(Event{At: time.Now(), Kind: "audit", From: "inventory", Action: "duplicate", OrderID: "ord-9"})
	st.add(Event{At: time.Now(), Kind: "audit", From: "inventory", Action: "redelivered", OrderID: "ord-9"})
	if v := st.sagaList()[0]; v.Duplicates != 1 || v.Redeliveries != 1 {
		t.Fatalf("dapat %+v", v)
	}
}
