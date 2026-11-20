package main

// Tipe status gabungan dan logika murni (tanpa I/O) yang diuji di status_test.go.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Tahap struct {
	Nama     string `json:"nama"`
	Status   string `json:"status"`
	DurasiMs int64  `json:"durasi_ms"`
}

type Build struct {
	Nomor    int     `json:"nomor"`
	Hasil    string  `json:"hasil"`
	Berjalan bool    `json:"berjalan"`
	Mulai    int64   `json:"mulai"`
	DurasiMs int64   `json:"durasi_ms"`
	Tag      string  `json:"tag"`
	Tahap    []Tahap `json:"tahap"`
}

type JenkinsInfo struct {
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Antre  bool    `json:"antre"`
	Builds []Build `json:"builds"`
}

type Operasi struct {
	Fase    string `json:"fase"`
	Pesan   string `json:"pesan"`
	Mulai   string `json:"mulai"`
	Selesai string `json:"selesai"`
}

type Pod struct {
	Nama   string `json:"nama"`
	Image  string `json:"image"`
	Tag    string `json:"tag"`
	Health string `json:"health"`
	Status string `json:"status,omitempty"`
	Dibuat string `json:"dibuat"`
}

type Deploy struct {
	ID     int64  `json:"id"`
	Revisi string `json:"revisi"`
	Tag    string `json:"tag"`
	Pesan  string `json:"pesan"`
	Waktu  string `json:"waktu"`
}

type ArgoInfo struct {
	OK           bool     `json:"ok"`
	Error        string   `json:"error,omitempty"`
	Sync         string   `json:"sync"`
	Health       string   `json:"health"`
	Revisi       string   `json:"revisi"`
	Rekonsiliasi string   `json:"rekonsiliasi"`
	Operasi      *Operasi `json:"operasi,omitempty"`
	Image        []string `json:"image"`
	Pods         []Pod    `json:"pods"`
	ReplicaSet   int      `json:"replicaset"`
	Riwayat      []Deploy `json:"riwayat"`
}

type Commit struct {
	SHA     string `json:"sha"`
	Pesan   string `json:"pesan"`
	Penulis string `json:"penulis"`
	Waktu   string `json:"waktu"`
}

type RepoInfo struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Commits []Commit `json:"commits"`
	Tag     string   `json:"tag,omitempty"`   // gitops: newTag saat ini
	Pesan   string   `json:"pesan,omitempty"` // app: isi konstanta Pesan saat ini
}

type Respon struct {
	Versi string `json:"versi"`
	Pesan string `json:"pesan"`
	Pod   string `json:"pod"`
}

type BerjalanInfo struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Sampel  int            `json:"sampel"`
	Gagal   int            `json:"gagal"`
	Versi   map[string]int `json:"versi"`
	Respons []Respon       `json:"respons"`
}

type Event struct {
	Waktu  time.Time `json:"waktu"`
	Sumber string    `json:"sumber"` // git, jenkins, gitops, argocd, k3s, aksi
	Pesan  string    `json:"pesan"`
}

type Tautan struct {
	UI, Gitea, GiteaApp, GiteaGitops, Jenkins, JenkinsJob, ArgoCD, ArgoApp, Registry, App, Grafana string
}

type Status struct {
	Waktu    time.Time    `json:"waktu"`
	Tautan   Tautan       `json:"tautan"`
	App      RepoInfo     `json:"app"`
	Jenkins  JenkinsInfo  `json:"jenkins"`
	Registry []string     `json:"registry"`
	GitOps   RepoInfo     `json:"gitops"`
	Argo     ArgoInfo     `json:"argo"`
	Berjalan BerjalanInfo `json:"berjalan"`
	Events   []Event      `json:"events"`
}

// ---------------------------------------------------------------- tag image

// Tag image dibuat Jenkinsfile: b<nomor build>-<7 karakter sha commit>.
var polaTag = regexp.MustCompile(`\bb[0-9]+-[0-9a-f]{7}\b`)

func tagDariTeks(s string) string { return polaTag.FindString(s) }

func tagDariImage(img string) string {
	if i := strings.LastIndex(img, ":"); i >= 0 && !strings.Contains(img[i:], "/") {
		return img[i+1:]
	}
	return ""
}

func nomorBuild(tag string) int {
	if !strings.HasPrefix(tag, "b") {
		return -1
	}
	n, err := strconv.Atoi(strings.SplitN(tag[1:], "-", 2)[0])
	if err != nil {
		return -1
	}
	return n
}

// urutkanTag: tag build terbaru (nomor terbesar) dulu.
func urutkanTag(t []string) {
	sort.SliceStable(t, func(i, j int) bool { return nomorBuild(t[i]) > nomorBuild(t[j]) })
}

func urutkanPod(p []Pod) { sort.Slice(p, func(i, j int) bool { return p[i].Nama < p[j].Nama }) }

func urutkanRespons(r []Respon) {
	sort.Slice(r, func(i, j int) bool { return r[i].Pod < r[j].Pod })
}

func barisPertama(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// tahapDariWf mengubah keluaran /wfapi. Stage Declarative yang dilewati karena stage sebelumnya
// gagal juga dilaporkan FAILED oleh pipeline-stage-view; di sini ditandai SKIPPED agar jelas
// bahwa hanya satu tahap yang benar-benar gagal.
func tahapDariWf(wf wfDescribe) []Tahap {
	var out []Tahap
	sudahGagal := false
	for _, s := range wf.Stages {
		st := s.Status
		if sudahGagal && st == "FAILED" {
			st = "SKIPPED"
		}
		if st == "FAILED" {
			sudahGagal = true
		}
		out = append(out, Tahap{Nama: s.Name, Status: st, DurasiMs: s.DurationMillis})
	}
	return out
}

// ---------------------------------------------------------------- isi berkas

var polaNewTag = regexp.MustCompile(`(?m)^(\s*newTag:\s*).*$`)

// gantiNewTag mengganti nilai newTag di kustomization.yaml; false bila baris tidak ada.
func gantiNewTag(isi, tag string) (string, bool) {
	if !polaNewTag.MatchString(isi) {
		return isi, false
	}
	return polaNewTag.ReplaceAllString(isi, "${1}"+tag), true
}

func bacaNewTag(isi string) string {
	m := polaNewTag.FindString(isi)
	if m == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(m, ":", 2)[1])
}

// kodePesan membuat isi pesan.go. strconv.Quote menjamin string Go yang sah apa pun isinya.
func kodePesan(pesan string) string {
	return "package main\n\n" +
		"// Pesan yang ditampilkan layanan. Tombol \"Ubah kode dan push\" di UI CI/CD Pipeline Viewer\n" +
		"// mengganti baris ini lewat commit ke repo app.\n" +
		"const Pesan = " + strconv.Quote(pesan) + "\n"
}

var polaPesan = regexp.MustCompile(`const Pesan = ("(?:[^"\\]|\\.)*")`)

func bacaPesan(kode string) string {
	m := polaPesan.FindStringSubmatch(kode)
	if m == nil {
		return ""
	}
	s, err := strconv.Unquote(m[1])
	if err != nil {
		return ""
	}
	return s
}

// ---------------------------------------------------------------- rollback

// pilihRollback memilih tag tujuan rollback: tag berbeda dari tag sekarang yang paling akhir
// pernah ter-deploy menurut riwayat Argo CD (urutan terbaru dulu).
func pilihRollback(riwayat []Deploy, sekarang string) string {
	for _, d := range riwayat {
		if d.Tag != "" && d.Tag != sekarang {
			return d.Tag
		}
	}
	return ""
}

// ---------------------------------------------------------------- deteksi event

// bandingkan menghasilkan event dari perbedaan dua status berturut-turut. Fungsi ini murni
// agar mudah diuji; waktu event diisi pemanggil.
func bandingkan(lama, baru *Status) []Event {
	var ev []Event
	tambah := func(sumber, f string, a ...any) { ev = append(ev, Event{Sumber: sumber, Pesan: fmt.Sprintf(f, a...)}) }
	if lama == nil {
		return nil
	}
	if c := commitBaru(lama.App.Commits, baru.App.Commits); c != nil {
		tambah("git", "push ke repo app %s: %s", pendek(c.SHA), c.Pesan)
	}
	lamaB := map[int]Build{}
	for _, b := range lama.Jenkins.Builds {
		lamaB[b.Nomor] = b
	}
	for i := len(baru.Jenkins.Builds) - 1; i >= 0; i-- {
		b := baru.Jenkins.Builds[i]
		l, ada := lamaB[b.Nomor]
		baruMuncul := !ada && lama.Jenkins.OK
		if baruMuncul {
			tambah("jenkins", "build #%d mulai", b.Nomor)
		}
		lt := map[string]string{}
		for _, t := range l.Tahap {
			lt[t.Nama] = t.Status
		}
		for _, t := range b.Tahap {
			if lt[t.Nama] != t.Status && t.Status != "IN_PROGRESS" {
				tambah("jenkins", "build #%d tahap \"%s\": %s", b.Nomor, t.Nama, t.Status)
			}
		}
		if (ada && l.Berjalan || baruMuncul) && !b.Berjalan && b.Hasil != "" {
			tambah("jenkins", "build #%d selesai: %s %s", b.Nomor, b.Hasil, b.Tag)
		}
	}
	if c := commitBaru(lama.GitOps.Commits, baru.GitOps.Commits); c != nil {
		tambah("gitops", "commit di repo gitops %s: %s", pendek(c.SHA), c.Pesan)
	}
	if baru.Argo.OK && lama.Argo.OK {
		if baru.Argo.Sync != lama.Argo.Sync {
			tambah("argocd", "sync status %s -> %s", lama.Argo.Sync, baru.Argo.Sync)
		}
		if baru.Argo.Health != lama.Argo.Health {
			tambah("argocd", "health %s -> %s", lama.Argo.Health, baru.Argo.Health)
		}
		if baru.Argo.Revisi != lama.Argo.Revisi && baru.Argo.Revisi != "" {
			tambah("argocd", "revisi Git yang di-sync: %s", pendek(baru.Argo.Revisi))
		}
		if len(baru.Argo.Pods) != len(lama.Argo.Pods) {
			tambah("k3s", "jumlah pod app %d -> %d", len(lama.Argo.Pods), len(baru.Argo.Pods))
		}
	}
	if v := versiDominan(baru.Berjalan.Versi); v != "" && v != versiDominan(lama.Berjalan.Versi) {
		tambah("k3s", "versi yang melayani request kini %s", v)
	}
	return ev
}

func commitBaru(lama, baru []Commit) *Commit {
	if len(baru) == 0 || len(lama) == 0 || baru[0].SHA == lama[0].SHA {
		return nil
	}
	return &baru[0]
}

// versiDominan: versi dengan jumlah jawaban terbanyak (seri dipecah berdasarkan nama).
func versiDominan(m map[string]int) string {
	terbaik, n := "", 0
	for v, c := range m {
		if c > n || c == n && v > terbaik {
			terbaik, n = v, c
		}
	}
	return terbaik
}

func pendek(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
