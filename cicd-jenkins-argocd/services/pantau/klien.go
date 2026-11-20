package main

// Klien HTTP kecil untuk Jenkins, Argo CD, Gitea, registry, dan aplikasi di k3s.
// Semua credential dibaca dari environment; tidak ada yang dikirim ke browser.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Konfig struct {
	GiteaURL, GiteaUser, GiteaPass     string
	JenkinsURL, JenkinsUser, JenkinsPW string
	ArgoURL, ArgoPass                  string
	RegistryURL                        string
	AppURL                             string
	Owner                              string
	PublicHost                         string
}

type Klien struct {
	k    Konfig
	http *http.Client
	// tanpa keep-alive: setiap sampel ke aplikasi membuka koneksi baru, sehingga kube-proxy
	// membagi request ke pod yang berbeda (terlihat saat rolling update).
	sampel *http.Client

	mu        sync.Mutex
	argoToken string
	// Argo CD mengunci akun sementara setelah beberapa login gagal; jeda sesudah gagal
	// mencegah pantau ikut memicu penguncian itu.
	loginGagal time.Time
}

func KlienBaru(k Konfig) *Klien {
	return &Klien{
		k:    k,
		http: &http.Client{Timeout: 5 * time.Second},
		sampel: &http.Client{Timeout: 1500 * time.Millisecond,
			Transport: &http.Transport{DisableKeepAlives: true}},
	}
}

var errTidakAda = errors.New("tidak ditemukan")

// minta menjalankan request dan mendekode JSON ke out (bila out bukan nil).
func (c *Klien) minta(ctx context.Context, hc *http.Client, metode, u string, body any, siapkan func(*http.Request), out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, metode, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if siapkan != nil {
		siapkan(req)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errTidakAda
	}
	if resp.StatusCode >= 300 {
		pesan, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &errHTTP{Kode: resp.StatusCode, Isi: string(pesan)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type errHTTP struct {
	Kode int
	Isi  string
}

func (e *errHTTP) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Kode, e.Isi) }

// ---------------------------------------------------------------- Jenkins

type jenkinsJob struct {
	InQueue bool `json:"inQueue"`
	Builds  []struct {
		Number      int    `json:"number"`
		Result      string `json:"result"`
		Building    bool   `json:"building"`
		Timestamp   int64  `json:"timestamp"`
		Duration    int64  `json:"duration"`
		Description string `json:"description"`
	} `json:"builds"`
}

type wfDescribe struct {
	Status string `json:"status"`
	Stages []struct {
		Name           string `json:"name"`
		Status         string `json:"status"`
		DurationMillis int64  `json:"durationMillis"`
	} `json:"stages"`
}

func (c *Klien) jenkins(ctx context.Context, path string, out any) error {
	return c.minta(ctx, c.http, http.MethodGet, c.k.JenkinsURL+path, nil, func(r *http.Request) {
		r.SetBasicAuth(c.k.JenkinsUser, c.k.JenkinsPW)
	}, out)
}

// AmbilJenkins membaca 8 build terakhir job "app" beserta tahapnya (plugin pipeline-stage-view, /wfapi).
func (c *Klien) AmbilJenkins(ctx context.Context) (JenkinsInfo, error) {
	var job jenkinsJob
	err := c.jenkins(ctx, "/job/app/api/json?tree=inQueue,builds[number,result,building,timestamp,duration,description]{0,8}", &job)
	if err != nil {
		return JenkinsInfo{}, err
	}
	info := JenkinsInfo{OK: true, Antre: job.InQueue}
	for i, b := range job.Builds {
		bd := Build{Nomor: b.Number, Hasil: b.Result, Berjalan: b.Building, Mulai: b.Timestamp,
			DurasiMs: b.Duration, Tag: b.Description}
		// tahap hanya diambil untuk 4 build terbaru agar ringan
		if i < 4 {
			var wf wfDescribe
			if err := c.jenkins(ctx, fmt.Sprintf("/job/app/%d/wfapi/describe", b.Number), &wf); err == nil {
				bd.Tahap = tahapDariWf(wf)
			}
		}
		if bd.Berjalan {
			bd.DurasiMs = time.Now().UnixMilli() - bd.Mulai
		}
		info.Builds = append(info.Builds, bd)
	}
	return info, nil
}

// ---------------------------------------------------------------- Gitea

type giteaCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type giteaIsi struct {
	SHA     string `json:"sha"`
	Content string `json:"content"`
}

func (c *Klien) gitea(ctx context.Context, metode, path string, body, out any) error {
	return c.minta(ctx, c.http, metode, c.k.GiteaURL+"/api/v1"+path, body, func(r *http.Request) {
		r.SetBasicAuth(c.k.GiteaUser, c.k.GiteaPass)
	}, out)
}

func (c *Klien) AmbilCommit(ctx context.Context, repo string, n int) ([]Commit, error) {
	var cs []giteaCommit
	path := fmt.Sprintf("/repos/%s/%s/commits?sha=main&limit=%d&stat=false&verification=false&files=false", c.k.Owner, repo, n)
	if err := c.gitea(ctx, http.MethodGet, path, nil, &cs); err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(cs))
	for _, g := range cs {
		out = append(out, Commit{SHA: g.SHA, Pesan: barisPertama(g.Commit.Message), Penulis: g.Commit.Author.Name,
			Waktu: g.Commit.Author.Date})
	}
	return out, nil
}

// BacaBerkas mengembalikan isi dan sha blob sebuah berkas di cabang main.
func (c *Klien) BacaBerkas(ctx context.Context, repo, path string) (string, string, error) {
	var isi giteaIsi
	if err := c.gitea(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/contents/%s?ref=main", c.k.Owner, repo, path), nil, &isi); err != nil {
		return "", "", err
	}
	b, err := base64.StdEncoding.DecodeString(isi.Content)
	return string(b), isi.SHA, err
}

// TulisBerkas membuat satu commit di cabang main yang mengganti isi berkas (Gitea contents API).
func (c *Klien) TulisBerkas(ctx context.Context, repo, path, sha, isi, pesan string) (string, error) {
	body := map[string]any{
		"content": base64.StdEncoding.EncodeToString([]byte(isi)),
		"sha":     sha,
		"message": pesan,
		"branch":  "main",
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err := c.gitea(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/%s/contents/%s", c.k.Owner, repo, path), body, &out)
	return out.Commit.SHA, err
}

// ---------------------------------------------------------------- Argo CD

type argoApp struct {
	Spec struct {
		Source struct {
			RepoURL string `json:"repoURL"`
		} `json:"source"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		OperationState *struct {
			Phase      string `json:"phase"`
			Message    string `json:"message"`
			StartedAt  string `json:"startedAt"`
			FinishedAt string `json:"finishedAt"`
		} `json:"operationState"`
		History []struct {
			ID         int64  `json:"id"`
			Revision   string `json:"revision"`
			DeployedAt string `json:"deployedAt"`
		} `json:"history"`
		Summary struct {
			Images []string `json:"images"`
		} `json:"summary"`
		ReconciledAt string `json:"reconciledAt"`
	} `json:"status"`
}

type argoTree struct {
	Nodes []struct {
		Kind      string   `json:"kind"`
		Name      string   `json:"name"`
		Namespace string   `json:"namespace"`
		Images    []string `json:"images"`
		CreatedAt string   `json:"createdAt"`
		Health    *struct {
			Status string `json:"status"`
		} `json:"health"`
		Info []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"info"`
	} `json:"nodes"`
}

func (c *Klien) masukArgo(ctx context.Context) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.minta(ctx, c.http, http.MethodPost, c.k.ArgoURL+"/api/v1/session",
		map[string]string{"username": "admin", "password": c.k.ArgoPass}, nil, &out)
	return out.Token, err
}

// argo memanggil API Argo CD dengan token sesi; token diperbarui sekali bila ditolak (401).
func (c *Klien) argo(ctx context.Context, metode, path string, body, out any) error {
	for coba := 0; coba < 2; coba++ {
		c.mu.Lock()
		tok := c.argoToken
		c.mu.Unlock()
		if tok == "" {
			c.mu.Lock()
			jeda := time.Since(c.loginGagal) < 30*time.Second
			c.mu.Unlock()
			if jeda {
				return errors.New("login Argo CD gagal; dicoba lagi sebentar lagi")
			}
			t, err := c.masukArgo(ctx)
			c.mu.Lock()
			if err != nil {
				var eh *errHTTP
				if errors.As(err, &eh) { // server menjawab tetapi menolak: jangan ulangi segera
					c.loginGagal = time.Now()
				}
				c.mu.Unlock()
				return fmt.Errorf("login Argo CD: %w", err)
			}
			c.argoToken, tok = t, t
			c.mu.Unlock()
		}
		err := c.minta(ctx, c.http, metode, c.k.ArgoURL+path, body, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+tok)
		}, out)
		var eh *errHTTP
		if errors.As(err, &eh) && (eh.Kode == http.StatusUnauthorized || eh.Kode == http.StatusForbidden) && coba == 0 {
			c.mu.Lock()
			c.argoToken = ""
			c.mu.Unlock()
			continue
		}
		return err
	}
	return nil
}

func (c *Klien) AmbilArgo(ctx context.Context) (ArgoInfo, error) {
	var app argoApp
	if err := c.argo(ctx, http.MethodGet, "/api/v1/applications/app", nil, &app); err != nil {
		return ArgoInfo{}, err
	}
	s := app.Status
	info := ArgoInfo{OK: true, Sync: s.Sync.Status, Health: s.Health.Status, Revisi: s.Sync.Revision,
		Image: s.Summary.Images, Rekonsiliasi: s.ReconciledAt}
	if s.OperationState != nil {
		info.Operasi = &Operasi{Fase: s.OperationState.Phase, Pesan: s.OperationState.Message,
			Mulai: s.OperationState.StartedAt, Selesai: s.OperationState.FinishedAt}
	}
	for i := len(s.History) - 1; i >= 0; i-- { // terbaru dulu
		h := s.History[i]
		info.Riwayat = append(info.Riwayat, Deploy{ID: h.ID, Revisi: h.Revision, Waktu: h.DeployedAt})
	}
	var tree argoTree
	if err := c.argo(ctx, http.MethodGet, "/api/v1/applications/app/resource-tree", nil, &tree); err == nil {
		for _, n := range tree.Nodes {
			switch n.Kind {
			case "Pod":
				p := Pod{Nama: n.Name, Dibuat: n.CreatedAt}
				if len(n.Images) > 0 {
					p.Image = n.Images[0]
					p.Tag = tagDariImage(n.Images[0])
				}
				if n.Health != nil {
					p.Health = n.Health.Status
				}
				for _, in := range n.Info {
					if in.Name == "Status Reason" {
						p.Status = in.Value
					}
				}
				info.Pods = append(info.Pods, p)
			case "ReplicaSet":
				info.ReplicaSet++
			}
		}
	}
	urutkanPod(info.Pods)
	return info, nil
}

// SkalaManual mengubah replicas Deployment langsung di cluster (lewat API Argo CD), sehingga
// cluster berbeda dari Git. selfHeal Argo CD harus mengembalikannya ke nilai di Git.
func (c *Klien) SkalaManual(ctx context.Context, replicas int) error {
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	q := url.Values{}
	q.Set("namespace", "demo")
	q.Set("resourceName", "app")
	q.Set("group", "apps")
	q.Set("version", "v1")
	q.Set("kind", "Deployment")
	q.Set("patchType", "application/merge-patch+json")
	// body gRPC-gateway untuk field "patch" (string): isi patch dikodekan sebagai string JSON.
	return c.argo(ctx, http.MethodPost, "/api/v1/applications/app/resource?"+q.Encode(), string(patch), nil)
}

// ---------------------------------------------------------------- registry dan aplikasi

func (c *Klien) AmbilTagRegistry(ctx context.Context) ([]string, error) {
	var out struct {
		Tags []string `json:"tags"`
	}
	err := c.minta(ctx, c.http, http.MethodGet, c.k.RegistryURL+"/v2/demo/app/tags/list", nil, nil, &out)
	if errors.Is(err, errTidakAda) {
		return []string{}, nil
	}
	urutkanTag(out.Tags)
	return out.Tags, err
}

type jawabanApp struct {
	Versi string `json:"versi"`
	Pesan string `json:"pesan"`
	Pod   string `json:"pod"`
}

// SampelApp mengirim n request ke Service NodePort aplikasi dan menghitung versi yang menjawab.
func (c *Klien) SampelApp(ctx context.Context, n int) BerjalanInfo {
	info := BerjalanInfo{Versi: map[string]int{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var j jawabanApp
			err := c.minta(ctx, c.sampel, http.MethodGet, c.k.AppURL+"/", nil, nil, &j)
			mu.Lock()
			defer mu.Unlock()
			info.Sampel++
			if err != nil {
				info.Gagal++
				info.Error = err.Error()
				return
			}
			info.Versi[j.Versi]++
			info.Respons = append(info.Respons, Respon{Versi: j.Versi, Pesan: j.Pesan, Pod: j.Pod})
		}()
	}
	wg.Wait()
	info.OK = info.Gagal < info.Sampel
	urutkanRespons(info.Respons)
	return info
}
