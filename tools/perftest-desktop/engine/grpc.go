package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func grpcDial(target string, useTLS, skipVerify bool) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if useTLS {
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: skipVerify}) //nolint:gosec // opsi lab
	}
	return grpc.NewClient(target, grpc.WithTransportCredentials(creds))
}

// splitMethod menerima "paket.Service/Method" atau "/paket.Service/Method" (juga "Service.Method").
func splitMethod(m string) (svc, method string, err error) {
	m = strings.TrimPrefix(strings.TrimSpace(m), "/")
	if i := strings.LastIndex(m, "/"); i > 0 {
		return m[:i], m[i+1:], nil
	}
	if i := strings.LastIndex(m, "."); i > 0 {
		return m[:i], m[i+1:], nil
	}
	return "", "", fmt.Errorf("grpcMethod harus berbentuk paket.Service/Method, dapat %q", m)
}

func resolveMethod(ctx context.Context, conn *grpc.ClientConn, full string) (protoreflect.MethodDescriptor, error) {
	svcName, mName, err := splitMethod(full)
	if err != nil {
		return nil, err
	}
	rc := grpcreflect.NewClientAuto(ctx, conn)
	defer rc.Reset()
	sd, err := rc.ResolveService(svcName)
	if err != nil {
		return nil, fmt.Errorf("server reflection: service %s: %w", svcName, err)
	}
	md := sd.FindMethodByName(mName)
	if md == nil {
		return nil, fmt.Errorf("method %s tidak ada di %s", mName, svcName)
	}
	d := md.UnwrapMethod()
	if d.IsStreamingClient() || d.IsStreamingServer() {
		return nil, fmt.Errorf("method %s streaming; perftest hanya mendukung unary", full)
	}
	return d, nil
}

type grpcTarget struct {
	conn    *grpc.ClientConn
	path    string
	in      proto.Message
	outDesc protoreflect.MessageDescriptor
}

func newGRPCTarget(ctx context.Context, cfg Config) (*grpcTarget, error) {
	conn, err := grpcDial(cfg.Target, cfg.TLS, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	md, err := resolveMethod(rctx, conn, cfg.GRPCMethod)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	in := dynamicpb.NewMessage(md.Input())
	req := strings.TrimSpace(cfg.Request)
	if req == "" {
		req = "{}"
	}
	if err := protojson.Unmarshal([]byte(req), in); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("request JSON tidak cocok dengan %s: %w", md.Input().FullName(), err)
	}
	return &grpcTarget{
		conn:    conn,
		path:    fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name()),
		in:      in,
		outDesc: md.Output(),
	}, nil
}

func (t *grpcTarget) NewWorker() Worker {
	// tiap worker memegang salinan pesan request sendiri (tidak berbagi state proto)
	return &grpcWorker{t: t, in: proto.Clone(t.in)}
}

func (t *grpcTarget) Close() error { return t.conn.Close() }

type grpcWorker struct {
	t  *grpcTarget
	in proto.Message
}

func (w *grpcWorker) Do(ctx context.Context) error {
	out := dynamicpb.NewMessage(w.t.outDesc)
	return w.t.conn.Invoke(ctx, w.t.path, w.in, out)
}

func (w *grpcWorker) Close() error { return nil }

// GRPCMethodInfo menjelaskan satu method unary hasil server reflection.
type GRPCMethodInfo struct {
	Method  string `json:"method"`  // paket.Service/Method
	Input   string `json:"input"`   // nama tipe pesan request
	Output  string `json:"output"`  // nama tipe pesan response
	Example string `json:"example"` // contoh JSON request (field kosong)
}

// ListGRPCMethods memanggil server reflection dan mengembalikan semua method unary.
func ListGRPCMethods(ctx context.Context, target string, useTLS, skipVerify bool) ([]GRPCMethodInfo, error) {
	conn, err := grpcDial(target, useTLS, skipVerify)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rc := grpcreflect.NewClientAuto(ctx, conn)
	defer rc.Reset()
	svcs, err := rc.ListServices()
	if err != nil {
		return nil, fmt.Errorf("server reflection: %w", err)
	}
	var out []GRPCMethodInfo
	for _, s := range svcs {
		if strings.HasPrefix(s, "grpc.reflection.") {
			continue
		}
		sd, err := rc.ResolveService(s)
		if err != nil {
			continue
		}
		for _, m := range sd.GetMethods() {
			d := m.UnwrapMethod()
			if d.IsStreamingClient() || d.IsStreamingServer() {
				continue
			}
			ex, _ := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(dynamicpb.NewMessage(d.Input()))
			out = append(out, GRPCMethodInfo{
				Method:  string(sd.UnwrapService().FullName()) + "/" + string(d.Name()),
				Input:   string(d.Input().FullName()),
				Output:  string(d.Output().FullName()),
				Example: string(ex),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}
