package main

import (
	"testing"

	"sister-bab05/services/platform"
)

func TestNextStep(t *testing.T) {
	cases := []struct {
		cur, subj, want, emit string
		ok                    bool
	}{
		{StatusPending, platform.SubjPaymentSucceeded, StatusConfirmed, platform.SubjOrderConfirmed, true},
		{StatusPending, platform.SubjInventoryRejected, StatusCancelled, platform.SubjOrderCancelled, true},
		{StatusPending, platform.SubjInventoryReleased, StatusCancelled, platform.SubjOrderCancelled, true},
		{StatusConfirmed, platform.SubjPaymentSucceeded, "", "", false}, // redelivery setelah final
		{StatusCancelled, platform.SubjInventoryReleased, "", "", false},
		{StatusPending, platform.SubjPaymentFailed, "", "", false}, // bukan urusan order
	}
	for _, c := range cases {
		got, ok := nextStep(c.cur, c.subj)
		if ok != c.ok || got.Next != c.want || got.Emit != c.emit {
			t.Errorf("%s + %s: dapat %+v ok=%v", c.cur, c.subj, got, ok)
		}
	}
}
