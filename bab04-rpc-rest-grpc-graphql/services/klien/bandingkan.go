package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/soap"
)

// Tampilan adalah bentuk mentah satu pesan: kepala (header) dan isi (body).
type Tampilan struct {
	Kepala string `json:"kepala"`
	Isi    string `json:"isi"`
	Biner  bool   `json:"biner"` // true: Isi berupa hex
}

// HasilProtokol adalah hasil satu protokol pada perbandingan.
type HasilProtokol struct {
	Protokol    string         `json:"protokol"`
	Label       string         `json:"label"`
	Format      string         `json:"format"`
	Transport   string         `json:"transport"`
	OK          bool           `json:"ok"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	Request     Tampilan       `json:"request"`
	Response    Tampilan       `json:"response"`
	PayloadReq  int            `json:"payload_req"`
	PayloadResp int            `json:"payload_resp"`
	WireReq     int            `json:"wire_req"`
	WireResp    int            `json:"wire_resp"`
	Latensi     Statistik      `json:"latensi"`
	Anotasi     []AnotasiBaris `json:"anotasi,omitempty"`
	Catatan     string         `json:"catatan,omitempty"`
}

// sekali adalah hasil satu panggilan.
type sekali struct {
	ok                bool
	status            string
	req, resp         Tampilan
	payReq, payResp   int
	wireReq, wireResp int
	anotasi           []AnotasiBaris
	catatan           string
}

type pemanggil struct {
	protokol, label, format, transport string
	panggil                            func(ctx context.Context, rinci bool) (sekali, error)
}

const batasTampil = 16 << 10

func potong(s string) string {
	if len(s) > batasTampil {
		return s[:batasTampil] + fmt.Sprintf("\n… (%d byte lagi)", len(s)-batasTampil)
	}
	return s
}

// panggilHTTP menjalankan satu request HTTP dan, bila rinci, menyimpan dump mentahnya.
func (k *Klien) panggilHTTP(ctx context.Context, rinci bool, buat func() (*http.Request, error), biner bool) (sekali, error) {
	req, err := buat()
	if err != nil {
		return sekali{}, err
	}
	req = req.WithContext(ctx)
	var s sekali
	var dumpReq []byte
	if rinci {
		// DumpRequestOut membaca body; buat ulang request untuk dikirim
		dumpReq, _ = httputil.DumpRequestOut(req, true)
		if req, err = buat(); err != nil {
			return s, err
		}
		req = req.WithContext(ctx)
	}
	if req.Body != nil && req.ContentLength > 0 {
		s.payReq = int(req.ContentLength)
	}
	resp, err := k.http.Do(req)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return s, err
	}
	s.payResp = len(body)
	s.status = resp.Status
	s.ok = resp.StatusCode < 400
	if rinci {
		kepala, _ := httputil.DumpResponse(resp, false)
		s.wireReq = len(dumpReq)
		s.wireResp = len(kepala) + len(body)
		i := bytes.Index(dumpReq, []byte("\r\n\r\n"))
		if i < 0 {
			i = len(dumpReq)
		}
		s.req = Tampilan{Kepala: string(dumpReq[:i]), Isi: potong(string(bytes.TrimPrefix(dumpReq[i:], []byte("\r\n\r\n"))))}
		s.resp = Tampilan{Kepala: strings.TrimSpace(string(kepala)), Isi: potong(string(body))}
		if biner {
			s.req.Isi, s.req.Biner = hexSpasi(bytes.TrimPrefix(dumpReq[i:], []byte("\r\n\r\n"))), true
			s.resp.Isi, s.resp.Biner = potong(hexSpasi(body)), true
		}
	}
	if biner {
		st := resp.Header.Get("Grpc-Status")
		s.ok = resp.StatusCode == http.StatusOK && (st == "0" || (st == "" && bytes.Contains(body, []byte("grpc-status:0"))))
		if rinci {
			s.anotasi, s.catatan = bongkarGRPCWeb(body)
		}
	}
	return s, nil
}

// bongkarGRPCWeb memisah frame gRPC-Web: [flag 1 byte][panjang 4 byte][isi].
// Flag 0x00 = pesan Protobuf, 0x80 = trailer (grpc-status dsb. sebagai teks).
func bongkarGRPCWeb(b []byte) ([]AnotasiBaris, string) {
	var a []AnotasiBaris
	var trailer string
	for len(b) >= 5 {
		flag, n := b[0], int(binary.BigEndian.Uint32(b[1:5]))
		if 5+n > len(b) {
			break
		}
		isi := b[5 : 5+n]
		if flag&0x80 != 0 {
			a = append(a, AnotasiBaris{hexSpasi(b[:5]), fmt.Sprintf("frame trailer, panjang %d", n), 0})
			trailer = strings.TrimSpace(string(isi))
		} else {
			a = append(a, AnotasiBaris{hexSpasi(b[:5]), fmt.Sprintf("frame data: flag 00, panjang %d", n), 0})
			batas := 40
			a = append(a, anotasiProto(isi, 1, &batas)...)
		}
		b = b[5+n:]
	}
	return a, "trailer: " + strings.ReplaceAll(trailer, "\r\n", "; ")
}

// rekamCodec adalah codec Protobuf yang menyimpan byte mentah request dan
// response, agar payload gRPC bisa ditampilkan apa adanya.
type rekamCodec struct{ req, resp []byte }

func (c *rekamCodec) Marshal(v any) ([]byte, error) {
	b, err := proto.Marshal(v.(proto.Message))
	c.req = b
	return b, err
}

func (c *rekamCodec) Unmarshal(d []byte, v any) error {
	c.resp = append([]byte(nil), d...)
	return proto.Unmarshal(d, v.(proto.Message))
}

func (c *rekamCodec) Name() string { return "proto" }

func kepalaMD(md metadata.MD) string {
	kunci := make([]string, 0, len(md))
	for k := range md {
		kunci = append(kunci, k)
	}
	sort.Strings(kunci)
	var sb strings.Builder
	for _, k := range kunci {
		fmt.Fprintf(&sb, "%s: %s\n", k, strings.Join(md[k], ", "))
	}
	return strings.TrimSpace(sb.String())
}

func (k *Klien) daftarPemanggil(operasi string, id int64, lewatGateway bool) []pemanggil {
	dasar := k.langsungHTTP
	gc := k.langsungGRPC
	if lewatGateway {
		dasar, gc = k.gwHTTP, k.gwGRPC
	}
	pathREST := fmt.Sprintf("/v1/produk/%d", id)
	gqlQuery := fmt.Sprintf(`{"query":"{ produk(id: %d) { id nama harga } }"}`, id)
	soapOp, soapIsi := "AmbilProduk", fmt.Sprintf("<id>%d</id>", id)
	metodeGRPC := "AmbilProduk"
	var reqPB proto.Message = &pb.AmbilProdukRequest{Id: id}
	if operasi == "daftar" {
		pathREST = "/v1/produk"
		gqlQuery = `{"query":"{ daftarProduk { id nama harga } }"}`
		soapOp, soapIsi = "DaftarProduk", ""
		metodeGRPC = "DaftarProduk"
		reqPB = &pb.DaftarProdukRequest{}
	}
	get := func(accept string) func() (*http.Request, error) {
		return func() (*http.Request, error) {
			r, err := http.NewRequest(http.MethodGet, dasar+pathREST, nil)
			if err == nil {
				r.Header.Set("Accept", accept)
			}
			return r, err
		}
	}
	daftar := []pemanggil{
		{"rest-json", "REST JSON", "JSON (teks)", "HTTP/1.1", func(ctx context.Context, rinci bool) (sekali, error) {
			return k.panggilHTTP(ctx, rinci, get("application/json"), false)
		}},
		{"rest-xml", "REST XML", "XML (teks)", "HTTP/1.1", func(ctx context.Context, rinci bool) (sekali, error) {
			return k.panggilHTTP(ctx, rinci, get("application/xml"), false)
		}},
		{"soap", "SOAP 1.1", "XML amplop", "HTTP/1.1", func(ctx context.Context, rinci bool) (sekali, error) {
			return k.panggilHTTP(ctx, rinci, func() (*http.Request, error) {
				r, err := http.NewRequest(http.MethodPost, dasar+"/soap", bytes.NewReader(soap.AmplopRequest(soapOp, soapIsi)))
				if err == nil {
					r.Header.Set("Content-Type", "text/xml; charset=utf-8")
					r.Header.Set("SOAPAction", `"`+soap.NS+"#"+soapOp+`"`)
				}
				return r, err
			}, false)
		}},
		{"graphql", "GraphQL", "JSON (teks)", "HTTP/1.1", func(ctx context.Context, rinci bool) (sekali, error) {
			return k.panggilHTTP(ctx, rinci, func() (*http.Request, error) {
				r, err := http.NewRequest(http.MethodPost, dasar+"/graphql", strings.NewReader(gqlQuery))
				if err == nil {
					r.Header.Set("Content-Type", "application/json")
				}
				return r, err
			}, false)
		}},
		{"grpc", "gRPC", "Protobuf (biner)", "HTTP/2", func(ctx context.Context, rinci bool) (sekali, error) {
			return panggilGRPC(ctx, gc, metodeGRPC, reqPB, rinci)
		}},
	}
	if lewatGateway {
		daftar = append(daftar, pemanggil{"grpc-web", "gRPC-Web", "Protobuf + frame", "HTTP/1.1", func(ctx context.Context, rinci bool) (sekali, error) {
			isi, _ := proto.Marshal(reqPB)
			frame := append([]byte{0, 0, 0, 0, 0}, isi...)
			binary.BigEndian.PutUint32(frame[1:5], uint32(len(isi)))
			return k.panggilHTTP(ctx, rinci, func() (*http.Request, error) {
				r, err := http.NewRequest(http.MethodPost, dasar+"/grpcweb/katalog.v1.KatalogService/"+metodeGRPC, bytes.NewReader(frame))
				if err == nil {
					r.Header.Set("Content-Type", "application/grpc-web+proto")
					r.Header.Set("X-Grpc-Web", "1")
				}
				return r, err
			}, true)
		}})
	}
	return daftar
}

func panggilGRPC(ctx context.Context, c pb.KatalogServiceClient, metode string, req proto.Message, rinci bool) (sekali, error) {
	codec := &rekamCodec{}
	var hdr, trl metadata.MD
	opsi := []grpc.CallOption{grpc.ForceCodec(codec), grpc.Header(&hdr), grpc.Trailer(&trl)}
	var err error
	switch metode {
	case "AmbilProduk":
		_, err = c.AmbilProduk(ctx, req.(*pb.AmbilProdukRequest), opsi...)
	default:
		_, err = c.DaftarProduk(ctx, req.(*pb.DaftarProdukRequest), opsi...)
	}
	s := sekali{ok: err == nil, status: "OK", payReq: len(codec.req), payResp: len(codec.resp)}
	if err != nil {
		s.status = err.Error()
	}
	if rinci {
		// gRPC length-prefixed message: 1 byte flag kompresi + 4 byte panjang
		s.wireReq, s.wireResp = len(codec.req)+5, len(codec.resp)+5
		s.req = Tampilan{Kepala: ":method: POST\n:path: /katalog.v1.KatalogService/" + metode +
			"\ncontent-type: application/grpc\nte: trailers", Isi: hexSpasi(codec.req), Biner: true}
		if s.req.Isi == "" {
			s.req.Isi = "(pesan kosong: 0 byte)"
		}
		s.resp = Tampilan{Kepala: kepalaMD(hdr) + "\n-- trailer --\n" + kepalaMD(trl), Isi: potong(hexSpasi(codec.resp)), Biner: true}
		batas := 40
		s.anotasi = anotasiProto(codec.resp, 0, &batas)
		s.catatan = "header HTTP/2 dikompres HPACK dan tidak dihitung; wire = payload + 5 byte prefix"
	}
	return s, nil
}

func (k *Klien) bandingkan(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Operasi string `json:"operasi"`
		ID      int64  `json:"id"`
		Ulang   int    `json:"ulang"`
		Jalur   string `json:"jalur"`
	}
	if bacaJSON(r, &q) != nil {
		tulisJSON(w, http.StatusBadRequest, map[string]string{"error": "json tidak sah"})
		return
	}
	if q.ID <= 0 {
		q.ID = 42
	}
	if q.Ulang <= 0 {
		q.Ulang = 20
	}
	if q.Ulang > 500 {
		q.Ulang = 500
	}
	if q.Operasi != "daftar" {
		q.Operasi = "ambil"
	}
	lewat := q.Jalur != "langsung"
	hasil := []HasilProtokol{}
	for _, p := range k.daftarPemanggil(q.Operasi, q.ID, lewat) {
		h := HasilProtokol{Protokol: p.protokol, Label: p.label, Format: p.format, Transport: p.transport}
		// panggilan pertama: rinci (dump mentah) dan sekaligus pemanasan koneksi
		ctx, b := context.WithTimeout(r.Context(), 5*time.Second)
		s, err := p.panggil(ctx, true)
		b()
		if err != nil {
			h.Error = err.Error()
			hasil = append(hasil, h)
			continue
		}
		h.OK, h.Status, h.Request, h.Response = s.ok, s.status, s.req, s.resp
		h.PayloadReq, h.PayloadResp, h.WireReq, h.WireResp = s.payReq, s.payResp, s.wireReq, s.wireResp
		h.Anotasi, h.Catatan = s.anotasi, s.catatan
		ms := make([]float64, 0, q.Ulang)
		for i := 0; i < q.Ulang; i++ {
			ctx, b := context.WithTimeout(r.Context(), 5*time.Second)
			mulai := time.Now()
			_, err := p.panggil(ctx, false)
			d := msSejak(mulai)
			b()
			if err != nil {
				h.Error = err.Error()
				break
			}
			ms = append(ms, d)
		}
		h.Latensi = hitungStatistik(ms)
		hasil = append(hasil, h)
	}
	tulisJSON(w, http.StatusOK, map[string]any{"operasi": q.Operasi, "id": q.ID, "ulang": q.Ulang,
		"jalur": map[bool]string{true: "gateway", false: "langsung"}[lewat], "hasil": hasil})
}
