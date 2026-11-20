package main

import (
	"strings"
	"testing"
)

func TestTagDariTeks(t *testing.T) {
	kasus := map[string]string{
		"deploy app b12-abc1234":                 "b12-abc1234",
		"rollback app ke b3-0011aaf (dari b5-x)": "b3-0011aaf",
		"awal: isi repo gitops":                  "",
	}
	for in, ingin := range kasus {
		if got := tagDariTeks(in); got != ingin {
			t.Errorf("tagDariTeks(%q) = %q, ingin %q", in, got, ingin)
		}
	}
	if got := tagDariImage("registry:5000/demo/app:b2-1234567"); got != "b2-1234567" {
		t.Errorf("tagDariImage = %q", got)
	}
	if got := tagDariImage("registry:5000/demo/app"); got != "" {
		t.Errorf("tagDariImage tanpa tag = %q", got)
	}
}

func TestUrutkanTag(t *testing.T) {
	tags := []string{"b2-aaaaaaa", "b10-bbbbbbb", "b1-ccccccc"}
	urutkanTag(tags)
	if strings.Join(tags, ",") != "b10-bbbbbbb,b2-aaaaaaa,b1-ccccccc" {
		t.Fatalf("urutan salah: %v", tags)
	}
}

func TestNewTag(t *testing.T) {
	isi := "images:\n  - name: registry:5000/demo/app\n    newTag: b1-aaaaaaa\n"
	baru, ok := gantiNewTag(isi, "b2-bbbbbbb")
	if !ok || bacaNewTag(baru) != "b2-bbbbbbb" || !strings.Contains(baru, "    newTag: b2-bbbbbbb\n") {
		t.Fatalf("gantiNewTag gagal: %q", baru)
	}
	if _, ok := gantiNewTag("resources: []\n", "x"); ok {
		t.Fatal("seharusnya false bila tidak ada newTag")
	}
}

func TestKodePesanBolakBalik(t *testing.T) {
	for _, p := range []string{"halo", `kutip "ganda" dan \ garis miring`, "", "baris\nbaru"} {
		if got := bacaPesan(kodePesan(p)); got != p {
			t.Errorf("bolak-balik %q -> %q", p, got)
		}
	}
}

func TestPilihRollback(t *testing.T) {
	r := []Deploy{{Tag: "b3-ccccccc"}, {Tag: "b3-ccccccc"}, {Tag: ""}, {Tag: "b2-bbbbbbb"}, {Tag: "b1-aaaaaaa"}}
	if got := pilihRollback(r, "b3-ccccccc"); got != "b2-bbbbbbb" {
		t.Fatalf("pilihRollback = %q", got)
	}
	if got := pilihRollback(r[:1], "b3-ccccccc"); got != "" {
		t.Fatalf("tanpa kandidat seharusnya kosong, dapat %q", got)
	}
}

func TestBandingkan(t *testing.T) {
	lama := &Status{
		App:      RepoInfo{Commits: []Commit{{SHA: "aaaaaaaaa"}}},
		Jenkins:  JenkinsInfo{OK: true, Builds: []Build{{Nomor: 1, Hasil: "SUCCESS"}}},
		Argo:     ArgoInfo{OK: true, Sync: "Synced", Health: "Healthy"},
		Berjalan: BerjalanInfo{Versi: map[string]int{"b1-aaaaaaa": 8}},
	}
	baru := &Status{
		App:      RepoInfo{Commits: []Commit{{SHA: "bbbbbbbbb", Pesan: "ubah pesan"}, {SHA: "aaaaaaaaa"}}},
		Jenkins:  JenkinsInfo{OK: true, Builds: []Build{{Nomor: 2, Berjalan: true, Tahap: []Tahap{{Nama: "Checkout", Status: "SUCCESS"}}}, {Nomor: 1, Hasil: "SUCCESS"}}},
		Argo:     ArgoInfo{OK: true, Sync: "OutOfSync", Health: "Healthy"},
		Berjalan: BerjalanInfo{Versi: map[string]int{"b1-aaaaaaa": 3, "b2-bbbbbbb": 5}},
	}
	ev := bandingkan(lama, baru)
	semua := ""
	for _, e := range ev {
		semua += e.Sumber + ":" + e.Pesan + "\n"
	}
	for _, ingin := range []string{"push ke repo app bbbbbbb", "build #2 mulai", `tahap "Checkout": SUCCESS`,
		"sync status Synced -> OutOfSync", "versi yang melayani request kini b2-bbbbbbb"} {
		if !strings.Contains(semua, ingin) {
			t.Errorf("event %q tidak muncul; dapat:\n%s", ingin, semua)
		}
	}
	if strings.Contains(semua, "build #1") {
		t.Errorf("build lama tidak boleh menghasilkan event:\n%s", semua)
	}
	if bandingkan(nil, baru) != nil {
		t.Error("tanpa status lama tidak ada event")
	}
}

func TestTahapDariWf(t *testing.T) {
	var wf wfDescribe
	for _, s := range []string{"SUCCESS", "FAILED", "FAILED", "FAILED"} {
		wf.Stages = append(wf.Stages, struct {
			Name           string `json:"name"`
			Status         string `json:"status"`
			DurationMillis int64  `json:"durationMillis"`
		}{Name: "x", Status: s})
	}
	got := tahapDariWf(wf)
	if got[1].Status != "FAILED" || got[2].Status != "SKIPPED" || got[3].Status != "SKIPPED" {
		t.Fatalf("status tahap: %+v", got)
	}
}
