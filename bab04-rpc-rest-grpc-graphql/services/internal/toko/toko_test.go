package toko

import "testing"

func TestProdukContoh(t *testing.T) {
	d := ProdukContoh()
	if len(d) != 50 {
		t.Fatalf("jumlah %d", len(d))
	}
	if p := d[41]; p.ID != 42 || p.Nama != "Kopi" || p.Harga != 25000 {
		t.Fatalf("produk 42: %+v", p)
	}
}

func TestSidik(t *testing.T) {
	if Sidik("budi", 25000) != Sidik("budi", 25000) {
		t.Fatal("sidik tidak deterministik")
	}
	if Sidik("budi", 25000) == Sidik("budi", 25001) {
		t.Fatal("isi berbeda harus beda sidik")
	}
}
