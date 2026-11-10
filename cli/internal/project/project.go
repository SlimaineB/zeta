package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = "zeta.yaml"

// Config mirrors zeta.yaml. Every field is optional; CLI flags take precedence.
type Config struct {
	Name    string   `yaml:"name"`
	Profile string   `yaml:"profile"`
	Image   string   `yaml:"image"`
	Context string   `yaml:"context"`
	Size    string   `yaml:"size"`
	Exclude []string `yaml:"exclude"`
	Env     EnvMap   `yaml:"env"`
}

// EnvMap accepts any scalar YAML value and stores it as a string, so
// `PORT: 8080` and `DEBUG: true` work as expected.
type EnvMap map[string]string

func (m *EnvMap) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("env must be a mapping")
	}
	out := make(EnvMap, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		out[node.Content[i].Value] = node.Content[i+1].Value
	}
	*m = out
	return nil
}

// Load reads zeta.yaml from dir (if present) and resolves the context root.
// It returns the config, the absolute context root, and any error.
func Load(dir string) (Config, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Config{}, "", err
	}
	data, err := os.ReadFile(filepath.Join(abs, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, abs, nil
	}
	if err != nil {
		return Config{}, "", err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("%s: %w", FileName, err)
	}
	root := abs
	if ctx := strings.TrimSpace(cfg.Context); ctx != "" {
		if filepath.IsAbs(ctx) {
			root = filepath.Clean(ctx)
		} else {
			root = filepath.Join(abs, ctx)
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Config{}, "", err
	}
	return cfg, root, nil
}
