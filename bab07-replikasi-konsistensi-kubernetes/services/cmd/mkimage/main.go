// mkimage: membuat image container (format docker-archive, sama dengan `docker save`) berisi satu
// binary statik, tanpa Docker daemon. Dipakai saat build agar image katalog dapat di-import
// otomatis oleh k3s dari /var/lib/rancher/k3s/agent/images/ tanpa registry.
package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func addFile(tw *tar.Writer, name string, data []byte, mode int64) error {
	h := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func main() {
	bin := flag.String("bin", "", "path binary")
	out := flag.String("o", "image.tar", "file keluaran")
	tag := flag.String("tag", "sister-bab07-katalog:v1", "nama image")
	arch := flag.String("arch", "amd64", "arsitektur")
	flag.Parse()
	data, err := os.ReadFile(*bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := filepath.Base(*bin)

	// Layer: satu tar berisi /<name>.
	var layer bytes.Buffer
	lw := tar.NewWriter(&layer)
	if err := addFile(lw, name, data, 0o755); err != nil {
		panic(err)
	}
	lw.Close()
	sum := sha256.Sum256(layer.Bytes())
	diffID := "sha256:" + hex.EncodeToString(sum[:])

	cfg := map[string]any{
		"architecture": *arch, "os": "linux", "created": "1970-01-01T00:00:00Z",
		"config": map[string]any{"Entrypoint": []string{"/" + name}, "User": "65534:65534",
			"Env": []string{"PATH=/"}, "ExposedPorts": map[string]any{"8080/tcp": map[string]any{}}},
		"rootfs":  map[string]any{"type": "layers", "diff_ids": []string{diffID}},
		"history": []map[string]string{{"created": "1970-01-01T00:00:00Z", "created_by": "mkimage"}},
	}
	cb, _ := json.Marshal(cfg)
	csum := sha256.Sum256(cb)
	cfgName := hex.EncodeToString(csum[:]) + ".json"
	layerName := hex.EncodeToString(sum[:]) + "/layer.tar"
	manifest, _ := json.Marshal([]map[string]any{{"Config": cfgName, "RepoTags": []string{*tag}, "Layers": []string{layerName}}})

	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for _, e := range []struct {
		n string
		b []byte
	}{{cfgName, cb}, {layerName, layer.Bytes()}, {"manifest.json", manifest}} {
		if err := addFile(tw, e.n, e.b, 0o644); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("image %s ditulis ke %s (%d byte layer)\n", *tag, *out, layer.Len())
}
