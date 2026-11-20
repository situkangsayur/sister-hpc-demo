package engine

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig membaca satu skenario dari file YAML (atau JSON, karena JSON adalah YAML).
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
