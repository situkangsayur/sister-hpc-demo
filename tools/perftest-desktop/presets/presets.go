// Package presets memuat daftar endpoint siap pakai untuk demo tiap bab.
package presets

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/situkangsayur/sister-hpc-demo/tools/perftest-desktop/engine"
)

//go:embed presets.yaml
var builtin []byte

// Preset adalah satu skenario bernama untuk bab tertentu (0 = umum).
type Preset struct {
	Bab    int           `json:"bab" yaml:"bab"`
	Name   string        `json:"name" yaml:"name"`
	Config engine.Config `json:"config" yaml:"config"`
}

type file struct {
	Presets []Preset `yaml:"presets"`
}

func parse(b []byte) ([]Preset, error) {
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for i := range f.Presets {
		if f.Presets[i].Config.Name == "" {
			f.Presets[i].Config.Name = f.Presets[i].Name
		}
	}
	return f.Presets, nil
}

// Builtin mengembalikan preset bawaan (presets.yaml yang di-embed).
func Builtin() []Preset {
	p, err := parse(builtin)
	if err != nil {
		panic(fmt.Sprintf("presets.yaml rusak: %v", err))
	}
	return p
}

// LoadFile membaca file preset YAML dengan format yang sama dengan presets.yaml.
func LoadFile(path string) ([]Preset, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(b)
}
