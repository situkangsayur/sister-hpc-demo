// pantau: backend CI/CD Pipeline Viewer. Setiap 2 detik membaca Gitea, Jenkins, registry,
// Argo CD, dan aplikasi di k3s, menyimpan status gabungan, dan mencatat event perubahan.
// Aksi demo (push kode, rollback, drift) dikerjakan di sini agar credential tetap di server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	berkasPesan  = "pesan.go"
	berkasKustom = "app/kustomization.yaml"
	maksEvent    = 120
)

var log = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "pantau")

type Server struct {
	c      *Klien
	mu     sync.RWMutex
	st     *Status
	ev     []Event
	aksiMu sync.Mutex // satu aksi tulis ke Git pada satu waktu
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func tautan(h string) Tautan {
	u := func(port int, path string) string { return fmt.Sprintf("http://%s:%d%s", h, port, path) }
	return Tautan{
		UI: u(51000, "/"), Gitea: u(51001, "/"), GiteaApp: u(51001, "/demo/app"), GiteaGitops: u(51001, "/demo/gitops"),
		Jenkins: u(51003, "/"), JenkinsJob: u(51003, "/job/app/"), ArgoCD: u(51006, "/"),
		ArgoApp: u(51006, "/applications/argocd/app"), Registry: u(51002, "/v2/demo/app/tags/list"),
		App: u(51005, "/"), Grafana: u(50000, "/"),
	}
}

// kumpulkan membaca semua sumber secara paralel; sumber yang gagal hanya ditandai error.
func (s *Server) kumpulkan(ctx context.Context) *Status {
	st := &Status{Waktu: time.Now().UTC(), Tautan: tautan(s.c.k.PublicHost), Registry: []string{}}
	var wg sync.WaitGroup
	jalan := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	jalan(func() {
		cs, err := s.c.AmbilCommit(ctx, "app", 6)
		st.App = RepoInfo{OK: err == nil, Commits: cs}
		if err != nil {
			st.App.Error = err.Error()
			return
		}
		if kode, _, err := s.c.BacaBerkas(ctx, "app", berkasPesan); err == nil {
			st.App.Pesan = bacaPesan(kode)
		}
	})
	jalan(func() {
		cs, err := s.c.AmbilCommit(ctx, "gitops", 12)
		st.GitOps = RepoInfo{OK: err == nil, Commits: cs}
		if err != nil {
			st.GitOps.Error = err.Error()
			return
		}
		if isi, _, err := s.c.BacaBerkas(ctx, "gitops", berkasKustom); err == nil {
			st.GitOps.Tag = bacaNewTag(isi)
		}
	})
	jalan(func() {
		j, err := s.c.AmbilJenkins(ctx)
		if err != nil {
			j.Error = err.Error()
		}
		st.Jenkins = j
	})
	jalan(func() {
		t, err := s.c.AmbilTagRegistry(ctx)
		if err == nil {
			st.Registry = t
		}
	})
	jalan(func() {
		a, err := s.c.AmbilArgo(ctx)
		if err != nil {
			a.Error = err.Error()
		}
		st.Argo = a
	})
	jalan(func() { st.Berjalan = s.c.SampelApp(ctx, 8) })
	wg.Wait()

	// Lengkapi riwayat deploy Argo CD dengan pesan commit gitops (berisi tag image).
	pesanCommit := map[string]string{}
	for _, c := range st.GitOps.Commits {
		pesanCommit[c.SHA] = c.Pesan
	}
	for i := range st.Argo.Riwayat {
		d := &st.Argo.Riwayat[i]
		d.Pesan = pesanCommit[d.Revisi]
		d.Tag = tagDariTeks(d.Pesan)
	}
	if st.Jenkins.Builds == nil {
		st.Jenkins.Builds = []Build{}
	}
	return st
}

func (s *Server) catat(sumber, pesan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tambahEvent(Event{Waktu: time.Now().UTC(), Sumber: sumber, Pesan: pesan})
}

func (s *Server) tambahEvent(e Event) {
	log.Info("event", "sumber", e.Sumber, "pesan", e.Pesan)
	s.ev = append([]Event{e}, s.ev...)
	if len(s.ev) > maksEvent {
		s.ev = s.ev[:maksEvent]
	}
}

func (s *Server) loop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		c, batal := context.WithTimeout(ctx, 8*time.Second)
		baru := s.kumpulkan(c)
		batal()
		s.mu.Lock()
		now := time.Now().UTC()
		for _, e := range bandingkan(s.st, baru) {
			e.Waktu = now
			s.tambahEvent(e)
		}
		s.st = baru
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func tulisJSON(w http.ResponseWriter, kode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(kode)
	_ = json.NewEncoder(w).Encode(v)
}

func gagal(w http.ResponseWriter, kode int, err error) {
	log.Warn("aksi gagal", "err", err)
	tulisJSON(w, kode, map[string]string{"error": err.Error()})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st == nil {
		tulisJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "belum ada data"})
		return
	}
	out := *s.st
	out.Events = s.ev
	tulisJSON(w, http.StatusOK, out)
}

// push: ubah konstanta Pesan di repo app (satu commit). gagal=true sengaja mengosongkan
// Pesan agar unit test di pipeline gagal dan image tidak pernah di-deploy.
func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pesan string `json:"pesan"`
		Gagal bool   `json:"gagal"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		gagal(w, http.StatusBadRequest, err)
		return
	}
	in.Pesan = strings.TrimSpace(in.Pesan)
	msg := "ubah pesan: " + in.Pesan
	if in.Gagal {
		in.Pesan, msg = "", "sengaja: kosongkan Pesan (unit test harus gagal)"
	} else if in.Pesan == "" || utf8.RuneCountInString(in.Pesan) > 120 {
		gagal(w, http.StatusBadRequest, errors.New("pesan wajib diisi, maksimal 120 karakter"))
		return
	}
	s.aksiMu.Lock()
	defer s.aksiMu.Unlock()
	_, sha, err := s.c.BacaBerkas(r.Context(), "app", berkasPesan)
	if err != nil {
		gagal(w, http.StatusBadGateway, err)
		return
	}
	commit, err := s.c.TulisBerkas(r.Context(), "app", berkasPesan, sha, kodePesan(in.Pesan), msg)
	if err != nil {
		gagal(w, http.StatusBadGateway, err)
		return
	}
	s.catat("aksi", fmt.Sprintf("UI membuat commit %s di repo app (%s)", pendek(commit), msg))
	tulisJSON(w, http.StatusOK, map[string]string{"commit": commit, "pesan": msg})
}

// rollback: cara GitOps, yaitu commit di repo gitops yang mengembalikan newTag ke tag lama.
// Argo CD (auto-sync) menolak rollback lewat API-nya, karena Git tetap sumber kebenaran.
func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tag string `json:"tag"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in)
	s.mu.RLock()
	st := s.st
	s.mu.RUnlock()
	if st == nil {
		gagal(w, http.StatusServiceUnavailable, errors.New("belum ada data"))
		return
	}
	if in.Tag == "" {
		in.Tag = pilihRollback(st.Argo.Riwayat, st.GitOps.Tag)
	}
	ada := false
	for _, t := range st.Registry {
		ada = ada || t == in.Tag
	}
	if !ada || tagDariTeks(in.Tag) != in.Tag {
		gagal(w, http.StatusBadRequest, fmt.Errorf("tag %q tidak ada di registry", in.Tag))
		return
	}
	s.aksiMu.Lock()
	defer s.aksiMu.Unlock()
	isi, sha, err := s.c.BacaBerkas(r.Context(), "gitops", berkasKustom)
	if err != nil {
		gagal(w, http.StatusBadGateway, err)
		return
	}
	lama := bacaNewTag(isi)
	if lama == in.Tag {
		gagal(w, http.StatusConflict, fmt.Errorf("tag %s sudah menjadi desired state", in.Tag))
		return
	}
	baru, _ := gantiNewTag(isi, in.Tag)
	msg := fmt.Sprintf("rollback app ke %s (dari %s)", in.Tag, lama)
	commit, err := s.c.TulisBerkas(r.Context(), "gitops", berkasKustom, sha, baru, msg)
	if err != nil {
		gagal(w, http.StatusBadGateway, err)
		return
	}
	s.catat("aksi", fmt.Sprintf("UI membuat commit %s di repo gitops: %s", pendek(commit), msg))
	tulisJSON(w, http.StatusOK, map[string]string{"commit": commit, "tag": in.Tag})
}

// drift: ubah cluster langsung (bukan lewat Git); selfHeal Argo CD harus mengembalikannya.
func (s *Server) drift(w http.ResponseWriter, r *http.Request) {
	if err := s.c.SkalaManual(r.Context(), 4); err != nil {
		gagal(w, http.StatusBadGateway, err)
		return
	}
	s.catat("aksi", "UI mengubah Deployment app langsung di cluster: replicas 4 (Git tetap 2)")
	tulisJSON(w, http.StatusOK, map[string]int{"replicas": 4})
}

func main() {
	k := Konfig{
		GiteaURL: env("GITEA_URL", "http://gitea:3000"), GiteaUser: os.Getenv("GITEA_ADMIN_USER"),
		GiteaPass: os.Getenv("GITEA_ADMIN_PASSWORD"), Owner: env("GITEA_ADMIN_USER", "demo"),
		JenkinsURL: env("JENKINS_URL", "http://jenkins:8080"), JenkinsUser: os.Getenv("JENKINS_ADMIN_USER"),
		JenkinsPW: os.Getenv("JENKINS_ADMIN_PASSWORD"),
		ArgoURL:   env("ARGOCD_URL", "http://k3s:30081"), ArgoPass: os.Getenv("ARGOCD_ADMIN_PASSWORD"),
		RegistryURL: env("REGISTRY_URL", "http://registry:5000"), AppURL: env("APP_URL", "http://k3s:30080"),
		PublicHost: env("PUBLIC_HOST", "127.0.0.1"),
	}
	s := &Server{c: KlienBaru(k)}
	go s.loop(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/push", s.push)
	mux.HandleFunc("POST /api/rollback", s.rollback)
	mux.HandleFunc("POST /api/drift", s.drift)
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Info("pantau mulai", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server berhenti", "err", err)
		os.Exit(1)
	}
}
