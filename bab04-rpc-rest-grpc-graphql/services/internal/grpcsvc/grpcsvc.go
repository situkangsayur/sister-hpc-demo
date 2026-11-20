// Paket grpcsvc adalah adapter gRPC untuk toko, dengan keempat mode panggilan
// (unary, server streaming, client streaming, bidirectional streaming),
// interceptor log, dan pemetaan error ke status code gRPC.
package grpcsvc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "sister/bab04/gen/katalogv1"
	"sister/bab04/internal/gangguan"
	"sister/bab04/internal/realtime"
	"sister/bab04/internal/toko"
)

// Server mengimplementasikan katalog.v1.KatalogService.
type Server struct {
	pb.UnimplementedKatalogServiceServer
	Toko     *toko.Toko
	Gangguan *gangguan.Penjaga
	Hub      *realtime.Hub
	Node     string
}

func keProto(p toko.Produk) *pb.Produk { return &pb.Produk{Id: p.ID, Nama: p.Nama, Harga: p.Harga} }

// keStatus memetakan error bisnis ke status code yang dapat ditindaklanjuti klien.
func keStatus(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, toko.ErrTidakAda):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, toko.ErrValidasi), errors.Is(err, toko.ErrKunciBeda):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, toko.ErrSaldo):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, toko.ErrMasihProses):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	slog.ErrorContext(ctx, "grpc: kesalahan internal", "err", err)
	return status.Error(codes.Internal, "kesalahan internal")
}

func (s *Server) AmbilProduk(ctx context.Context, r *pb.AmbilProdukRequest) (*pb.Produk, error) {
	p, err := s.Toko.Ambil(ctx, r.GetId())
	if err != nil {
		return nil, keStatus(ctx, err)
	}
	return keProto(p), nil
}

func (s *Server) DaftarProduk(ctx context.Context, _ *pb.DaftarProdukRequest) (*pb.DaftarProdukResponse, error) {
	d, err := s.Toko.Daftar(ctx)
	if err != nil {
		return nil, keStatus(ctx, err)
	}
	hasil := make([]*pb.Produk, len(d))
	for i, p := range d {
		hasil[i] = keProto(p)
	}
	return &pb.DaftarProdukResponse{Produk: hasil}, nil
}

func (s *Server) UbahHarga(ctx context.Context, r *pb.UbahHargaRequest) (*pb.Produk, error) {
	p, err := s.Toko.UbahHarga(toko.DenganOleh(ctx, "grpc"), r.GetId(), r.GetHarga())
	if err != nil {
		return nil, keStatus(ctx, err)
	}
	return keProto(p), nil
}

// Bayar membaca idempotency key dari metadata "idempotency-key".
func (s *Server) Bayar(ctx context.Context, r *pb.BayarRequest) (*pb.BayarResponse, error) {
	if s.Gangguan.HarusGagal(ctx) {
		return nil, status.Error(codes.Unavailable, "service sedang gangguan (fault injection), belum diproses")
	}
	kunci := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get("idempotency-key")) > 0 {
		kunci = md.Get("idempotency-key")[0]
	}
	kerja, batal := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer batal()
	h, err := s.Toko.Bayar(kerja, kunci, r.GetAkun(), r.GetJumlah())
	lambat := s.Gangguan.AmbilLambat(kerja)
	slog.InfoContext(ctx, "bayar", "protokol", "grpc", "akun", r.GetAkun(), "jumlah", r.GetJumlah(),
		"idempotency_key", kunci, "dari_cache", h.DariCache, "err", errString(err))
	if lambat > 0 {
		select {
		case <-time.After(lambat):
		case <-ctx.Done(): // deadline klien lewat: response tidak pernah sampai
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if err != nil {
		return nil, keStatus(ctx, err)
	}
	return &pb.BayarResponse{Sisa: h.Sisa, IdBayar: h.IDBayar, DariCache: h.DariCache}, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// PantauHarga (server streaming): kirim harga sekarang, lalu setiap perubahan.
func (s *Server) PantauHarga(r *pb.PantauHargaRequest, st grpc.ServerStreamingServer[pb.Produk]) error {
	ctx := st.Context()
	ids := map[int64]bool{}
	for _, id := range r.GetId() {
		ids[id] = true
		if p, err := s.Toko.Ambil(ctx, id); err == nil {
			if err := st.Send(keProto(p)); err != nil {
				return err
			}
		}
	}
	c, lepas := s.Hub.Langganan()
	defer lepas()
	for {
		select {
		case <-ctx.Done():
			return nil
		case p := <-c:
			if p.Saluran != toko.SaluranHarga {
				continue
			}
			var ph toko.PerubahanHarga
			if json.Unmarshal([]byte(p.Isi), &ph) != nil || (len(ids) > 0 && !ids[ph.ID]) {
				continue
			}
			if err := st.Send(keProto(ph.Produk)); err != nil {
				return err
			}
		}
	}
}

// CatatPenjualan (client streaming): klien mengirim banyak pesan, server
// menjumlahkan lalu menjawab sekali saat klien menutup stream.
func (s *Server) CatatPenjualan(st grpc.ClientStreamingServer[pb.Penjualan, pb.RingkasanPenjualan]) error {
	var r pb.RingkasanPenjualan
	harga := map[int64]int64{}
	for {
		m, err := st.Recv()
		if err == io.EOF {
			return st.SendAndClose(&r)
		}
		if err != nil {
			return err
		}
		if _, ada := harga[m.GetProdukId()]; !ada {
			p, err := s.Toko.Ambil(st.Context(), m.GetProdukId())
			if err != nil {
				return keStatus(st.Context(), err)
			}
			harga[p.ID] = p.Harga
		}
		r.JumlahPesan++
		r.TotalUnit += m.GetJumlah()
		r.TotalRupiah += m.GetJumlah() * harga[m.GetProdukId()]
	}
}

// Tawar (bidirectional streaming): setiap tawaran langsung dijawab; tawaran
// diterima bila minimal 90% harga.
func (s *Server) Tawar(st grpc.BidiStreamingServer[pb.Tawaran, pb.JawabanTawaran]) error {
	for {
		m, err := st.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := s.Toko.Ambil(st.Context(), m.GetProdukId())
		if err != nil {
			return keStatus(st.Context(), err)
		}
		j := &pb.JawabanTawaran{ProdukId: p.ID, Diterima: m.GetHarga()*10 >= p.Harga*9}
		if j.Diterima {
			j.Pesan = "deal: " + p.Nama
		} else {
			j.Pesan = "terlalu rendah untuk " + p.Nama
		}
		if err := st.Send(j); err != nil {
			return err
		}
	}
}

// requestID membaca x-request-id dari metadata (dipasang APISIX).
func requestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("x-request-id"); len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// Interceptor unary: pasang header x-node dan catat log JSON.
func Unary(node string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		mulai := time.Now()
		grpc.SetHeader(ctx, metadata.Pairs("x-node", node))
		resp, err := h(ctx, req)
		if info.FullMethod != "/grpc.health.v1.Health/Check" {
			slog.InfoContext(ctx, "grpc", "method", info.FullMethod, "code", status.Code(err).String(),
				"durasi_ms", float64(time.Since(mulai).Microseconds())/1000, "request_id", requestID(ctx))
		}
		return resp, err
	}
}

// Stream: interceptor untuk panggilan streaming.
func Stream(node string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		mulai := time.Now()
		ss.SetHeader(metadata.Pairs("x-node", node))
		err := h(srv, ss)
		slog.InfoContext(ss.Context(), "grpc stream", "method", info.FullMethod, "code", status.Code(err).String(),
			"durasi_ms", float64(time.Since(mulai).Microseconds())/1000, "request_id", requestID(ss.Context()))
		return err
	}
}
