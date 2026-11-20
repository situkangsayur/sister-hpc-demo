package main

import "sister-bab05/services/platform"

// Status pesanan. Saga tidak punya isolasi ACID, jadi status perantara dibuat eksplisit.
const (
	StatusPending   = "PENDING"
	StatusConfirmed = "CONFIRMED"
	StatusCancelled = "CANCELLED"
)

// sagaStep adalah transisi yang dilakukan order ketika menerima sebuah event.
type sagaStep struct {
	Next   string // status baru
	Emit   string // event yang diterbitkan (lewat outbox)
	Reason string
}

// nextStep adalah bagian order dari saga choreography (lihat Gambar saga di Bab 5):
//
//	payment.succeeded  : PENDING -> CONFIRMED, terbitkan order.confirmed (T4)
//	inventory.rejected : PENDING -> CANCELLED, terbitkan order.cancelled (stok habis, tidak ada yang perlu dikompensasi)
//	inventory.released : PENDING -> CANCELLED, terbitkan order.cancelled (C1, setelah C2 di inventory)
//
// Event yang datang ketika status sudah final diabaikan (ok=false), sehingga redelivery aman.
func nextStep(current, subject string) (sagaStep, bool) {
	if current != StatusPending {
		return sagaStep{}, false
	}
	switch subject {
	case platform.SubjPaymentSucceeded:
		return sagaStep{StatusConfirmed, platform.SubjOrderConfirmed, "pembayaran berhasil"}, true
	case platform.SubjInventoryRejected:
		return sagaStep{StatusCancelled, platform.SubjOrderCancelled, "stok tidak cukup"}, true
	case platform.SubjInventoryReleased:
		return sagaStep{StatusCancelled, platform.SubjOrderCancelled, "pembayaran gagal, stok sudah dikembalikan (kompensasi)"}, true
	}
	return sagaStep{}, false
}
