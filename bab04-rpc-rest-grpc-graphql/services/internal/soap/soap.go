// Paket soap adalah adapter SOAP 1.1 untuk toko: contoh "era XML" di buku
// (Subbab Era XML dan era biner). Kontraknya ditulis dalam WSDL dan dilayani di
// GET /soap?wsdl. Setiap panggilan adalah POST ke satu endpoint /soap dengan
// header SOAPAction dan amplop XML; error dikirim sebagai soap:Fault.
package soap

import (
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"sister/bab04/internal/toko"
)

const nsSoap = "http://schemas.xmlsoap.org/soap/envelope/"

// NS adalah namespace target layanan katalog.
const NS = "urn:sister:katalog:v1"

// Server melayani SOAP.
type Server struct {
	Toko *toko.Toko
}

type amplopMasuk struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

type ambilReq struct {
	XMLName xml.Name `xml:"AmbilProduk"`
	ID      int64    `xml:"id"`
}

type ubahReq struct {
	XMLName xml.Name `xml:"UbahHarga"`
	ID      int64    `xml:"id"`
	Harga   int64    `xml:"harga"`
}

// produkSoap adalah bentuk produk di dalam amplop.
type produkSoap struct {
	ID    int64  `xml:"id"`
	Nama  string `xml:"nama"`
	Harga int64  `xml:"harga"`
}

func keSoap(p toko.Produk) produkSoap { return produkSoap{p.ID, p.Nama, p.Harga} }

type ambilResp struct {
	XMLName xml.Name   `xml:"AmbilProdukResponse"`
	NS      string     `xml:"xmlns,attr"`
	Produk  produkSoap `xml:"produk"`
}

type daftarResp struct {
	XMLName xml.Name     `xml:"DaftarProdukResponse"`
	NS      string       `xml:"xmlns,attr"`
	Produk  []produkSoap `xml:"produk"`
}

type ubahResp struct {
	XMLName xml.Name   `xml:"UbahHargaResponse"`
	NS      string     `xml:"xmlns,attr"`
	Produk  produkSoap `xml:"produk"`
}

type fault struct {
	XMLName xml.Name `xml:"soap:Fault"`
	Code    string   `xml:"faultcode"`
	String  string   `xml:"faultstring"`
}

type amplopKeluar struct {
	XMLName xml.Name `xml:"soap:Envelope"`
	NSSoap  string   `xml:"xmlns:soap,attr"`
	Body    struct {
		Isi any
	} `xml:"soap:Body"`
}

// Amplop membungkus isi ke dalam soap:Envelope/soap:Body.
func Amplop(isi any) []byte {
	a := amplopKeluar{NSSoap: nsSoap}
	a.Body.Isi = isi
	b, _ := xml.Marshal(a)
	return append([]byte(xml.Header), b...)
}

// AmplopRequest membuat amplop request (dipakai klien playground).
func AmplopRequest(operasi string, isi string) []byte {
	return []byte(xml.Header + `<soap:Envelope xmlns:soap="` + nsSoap + `"><soap:Body><` + operasi +
		` xmlns="` + NS + `">` + isi + `</` + operasi + `></soap:Body></soap:Envelope>`)
}

func tulis(w http.ResponseWriter, status int, isi any) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	w.Write(Amplop(isi))
}

func tulisFault(w http.ResponseWriter, kode, pesan string) {
	tulis(w, http.StatusInternalServerError, fault{Code: kode, String: pesan}) // SOAP 1.1: fault = HTTP 500
}

// Daftarkan memasang route /soap.
func (s *Server) Daftarkan(mux *http.ServeMux) {
	mux.HandleFunc("GET /soap", s.wsdl)
	mux.HandleFunc("POST /soap", s.layani)
}

func (s *Server) wsdl(w http.ResponseWriter, r *http.Request) {
	if _, ada := r.URL.Query()["wsdl"]; !ada {
		http.Error(w, "pakai GET /soap?wsdl untuk kontrak WSDL, atau POST /soap untuk memanggil", http.StatusBadRequest)
		return
	}
	alamat := "http://" + r.Host + "/soap"
	if p := r.Header.Get("X-Forwarded-Prefix"); p != "" {
		alamat = "http://" + r.Host + p + "/soap"
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	io.WriteString(w, strings.ReplaceAll(WSDL, "{{ALAMAT}}", alamat))
}

func (s *Server) layani(w http.ResponseWriter, r *http.Request) {
	var a amplopMasuk
	if err := xml.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&a); err != nil {
		tulisFault(w, "soap:Client", "amplop SOAP tidak sah")
		return
	}
	isi := a.Body.Inner
	aksi := strings.Trim(r.Header.Get("SOAPAction"), `"`)
	op := operasi(isi)
	if op == "" { // body kosong: pakai SOAPAction
		op = aksi[strings.LastIndexAny(aksi, "#/")+1:]
	}
	ctx := r.Context()
	switch op {
	case "AmbilProduk":
		var q ambilReq
		if xml.Unmarshal(isi, &q) != nil {
			tulisFault(w, "soap:Client", "AmbilProduk butuh elemen id")
			return
		}
		p, err := s.Toko.Ambil(ctx, q.ID)
		if errors.Is(err, toko.ErrTidakAda) {
			tulisFault(w, "soap:Client", "produk tidak ditemukan")
			return
		}
		if err != nil {
			slog.ErrorContext(ctx, "soap", "err", err)
			tulisFault(w, "soap:Server", "kesalahan internal")
			return
		}
		tulis(w, http.StatusOK, ambilResp{NS: NS, Produk: keSoap(p)})
	case "DaftarProduk":
		d, err := s.Toko.Daftar(ctx)
		if err != nil {
			tulisFault(w, "soap:Server", "kesalahan internal")
			return
		}
		hasil := make([]produkSoap, len(d))
		for i, p := range d {
			hasil[i] = keSoap(p)
		}
		tulis(w, http.StatusOK, daftarResp{NS: NS, Produk: hasil})
	case "UbahHarga":
		var q ubahReq
		if xml.Unmarshal(isi, &q) != nil {
			tulisFault(w, "soap:Client", "UbahHarga butuh elemen id dan harga")
			return
		}
		p, err := s.Toko.UbahHarga(toko.DenganOleh(ctx, "soap"), q.ID, q.Harga)
		if err != nil {
			tulisFault(w, "soap:Client", err.Error())
			return
		}
		tulis(w, http.StatusOK, ubahResp{NS: NS, Produk: keSoap(p)})
	default:
		tulisFault(w, "soap:Client", "operasi tidak dikenal: "+op)
	}
}

// operasi membaca nama lokal elemen pertama di dalam soap:Body.
func operasi(isi []byte) string {
	d := xml.NewDecoder(strings.NewReader(string(isi)))
	for {
		t, err := d.Token()
		if err != nil {
			return ""
		}
		if se, ok := t.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

// WSDL 1.1 layanan katalog (document/literal).
const WSDL = `<?xml version="1.0" encoding="UTF-8"?>
<definitions name="Katalog"
  targetNamespace="urn:sister:katalog:v1"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:sister:katalog:v1"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:sister:katalog:v1" elementFormDefault="qualified">
      <xsd:complexType name="Produk">
        <xsd:sequence>
          <xsd:element name="id" type="xsd:long"/>
          <xsd:element name="nama" type="xsd:string"/>
          <xsd:element name="harga" type="xsd:long"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:element name="AmbilProduk">
        <xsd:complexType><xsd:sequence><xsd:element name="id" type="xsd:long"/></xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="AmbilProdukResponse">
        <xsd:complexType><xsd:sequence><xsd:element name="produk" type="tns:Produk"/></xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="DaftarProduk"><xsd:complexType/></xsd:element>
      <xsd:element name="DaftarProdukResponse">
        <xsd:complexType><xsd:sequence><xsd:element name="produk" type="tns:Produk" maxOccurs="unbounded"/></xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="UbahHarga">
        <xsd:complexType><xsd:sequence>
          <xsd:element name="id" type="xsd:long"/><xsd:element name="harga" type="xsd:long"/>
        </xsd:sequence></xsd:complexType>
      </xsd:element>
      <xsd:element name="UbahHargaResponse">
        <xsd:complexType><xsd:sequence><xsd:element name="produk" type="tns:Produk"/></xsd:sequence></xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="AmbilProdukInput"><part name="parameters" element="tns:AmbilProduk"/></message>
  <message name="AmbilProdukOutput"><part name="parameters" element="tns:AmbilProdukResponse"/></message>
  <message name="DaftarProdukInput"><part name="parameters" element="tns:DaftarProduk"/></message>
  <message name="DaftarProdukOutput"><part name="parameters" element="tns:DaftarProdukResponse"/></message>
  <message name="UbahHargaInput"><part name="parameters" element="tns:UbahHarga"/></message>
  <message name="UbahHargaOutput"><part name="parameters" element="tns:UbahHargaResponse"/></message>
  <portType name="KatalogPortType">
    <operation name="AmbilProduk"><input message="tns:AmbilProdukInput"/><output message="tns:AmbilProdukOutput"/></operation>
    <operation name="DaftarProduk"><input message="tns:DaftarProdukInput"/><output message="tns:DaftarProdukOutput"/></operation>
    <operation name="UbahHarga"><input message="tns:UbahHargaInput"/><output message="tns:UbahHargaOutput"/></operation>
  </portType>
  <binding name="KatalogBinding" type="tns:KatalogPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="AmbilProduk"><soap:operation soapAction="urn:sister:katalog:v1#AmbilProduk"/>
      <input><soap:body use="literal"/></input><output><soap:body use="literal"/></output></operation>
    <operation name="DaftarProduk"><soap:operation soapAction="urn:sister:katalog:v1#DaftarProduk"/>
      <input><soap:body use="literal"/></input><output><soap:body use="literal"/></output></operation>
    <operation name="UbahHarga"><soap:operation soapAction="urn:sister:katalog:v1#UbahHarga"/>
      <input><soap:body use="literal"/></input><output><soap:body use="literal"/></output></operation>
  </binding>
  <service name="KatalogService">
    <port name="KatalogPort" binding="tns:KatalogBinding"><soap:address location="{{ALAMAT}}"/></port>
  </service>
</definitions>
`
