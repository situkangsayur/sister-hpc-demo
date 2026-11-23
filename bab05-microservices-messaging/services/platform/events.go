package platform

// Subject event saga (JetStream stream SHOP). Nama event berbentuk lampau: fakta yang sudah terjadi.
const (
	SubjOrderCreated      = "shop.order.created"      // T1 order -> inventory
	SubjInventoryReserved = "shop.inventory.reserved" // T2 inventory -> payment
	SubjInventoryRejected = "shop.inventory.rejected" // T2 gagal -> order
	SubjPaymentSucceeded  = "shop.payment.succeeded"  // T3 payment -> order
	SubjPaymentFailed     = "shop.payment.failed"     // T3 gagal -> inventory (C2)
	SubjInventoryReleased = "shop.inventory.released" // C2 selesai -> order (C1)
	SubjOrderConfirmed    = "shop.order.confirmed"    // T4 order -> notification, inventory
	SubjOrderCancelled    = "shop.order.cancelled"    // C1 order -> notification
)

// Item adalah satu baris keranjang.
type Item struct {
	ProductID string `json:"product_id"`
	Qty       int    `json:"qty"`
	Price     int    `json:"price,omitempty"`
}

// OrderEvent membawa state yang dibutuhkan penerima (event-carried state transfer),
// sehingga inventory dan payment tidak perlu bertanya balik ke order.
type OrderEvent struct {
	OrderID  string `json:"order_id"`
	Customer string `json:"customer,omitempty"`
	Items    []Item `json:"items,omitempty"`
	Total    int    `json:"total,omitempty"`
	Card     string `json:"card,omitempty"`
	// NoRekening diisi bila pembeli membayar dari rekening bank (service bank); kosong = kartu.
	NoRekening string `json:"no_rekening,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Product adalah produk katalog. Daftar yang sama dipakai catalog (harga, OpenSearch)
// dan inventory (stok awal).
type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Price       int    `json:"price"`
}

// SeedProducts adalah isi awal toko daring.
var SeedProducts = []Product{
	{"P001", "Kopi Arabika Gayo 250 g", "Biji kopi sangrai medium dari dataran tinggi Gayo, Aceh", "minuman", 85000},
	{"P002", "Kopi Robusta Lampung 500 g", "Kopi bubuk robusta, rasa pahit tebal, cocok untuk kopi tubruk", "minuman", 60000},
	{"P003", "Teh Melati Tubruk", "Teh hijau dengan bunga melati, seduh langsung di gelas", "minuman", 18000},
	{"P004", "Cokelat Bubuk Jember", "Bubuk kakao murni tanpa gula dari kebun Jember", "minuman", 45000},
	{"P005", "Madu Hutan Sumbawa 350 ml", "Madu liar dari hutan Sumbawa, tanpa campuran gula", "makanan", 120000},
	{"P006", "Keripik Tempe Malang", "Keripik tempe tipis renyah, bumbu bawang", "makanan", 22000},
	{"P007", "Sambal Roa Manado", "Sambal ikan roa asap, pedas gurih, botol kaca", "makanan", 55000},
	{"P008", "Rendang Kering Padang 250 g", "Rendang daging sapi dimasak kering, tahan lama", "makanan", 150000},
	{"P009", "Batik Tulis Pekalongan", "Kain batik tulis motif jlamprang, katun primisima", "fashion", 450000},
	{"P010", "Kemeja Tenun Ikat Sumba", "Kemeja lengan pendek dari kain tenun ikat Sumba", "fashion", 375000},
	{"P011", "Tas Anyaman Pandan", "Tas jinjing anyaman daun pandan dari Tasikmalaya", "fashion", 135000},
	{"P012", "Sandal Kulit Garut", "Sandal kulit domba buatan pengrajin Garut", "fashion", 175000},
	{"P013", "Buku Sistem Terdistribusi", "Buku ajar sistem terdistribusi dan komputasi berkinerja tinggi", "buku", 98000},
	{"P014", "Buku Pemrograman Go", "Pengantar bahasa Go: goroutine, channel, dan service HTTP", "buku", 89000},
	{"P015", "Raspberry Pi 5 4 GB", "Komputer papan tunggal untuk belajar klaster kecil", "elektronik", 1350000},
	{"P016", "Switch Gigabit 8 Port", "Switch jaringan unmanaged 8 port untuk lab klaster", "elektronik", 320000},
	{"P017", "Kabel LAN Cat6 5 m", "Kabel jaringan Cat6 dengan konektor RJ45", "elektronik", 45000},
	{"P018", "SSD NVMe 1 TB", "Penyimpanan NVMe PCIe 4.0 untuk server lab", "elektronik", 1150000},
	{"P019", "Gantungan Kunci Wayang", "Gantungan kunci kulit motif wayang Arjuna", "suvenir", 15000},
	{"P020", "Miniatur Rumah Gadang", "Miniatur rumah adat Minangkabau dari kayu", "suvenir", 210000},
}
