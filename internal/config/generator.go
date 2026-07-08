package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest represents the phase-to-agent mapping.
type Manifest struct {
	Phases map[int][]string `yaml:"phases"`
}

// WriteManifest writes the manifest to .aa/templates/manifest.yaml
func WriteManifest(projectRoot string, m Manifest) error {
	dir := filepath.Join(projectRoot, ".aa", "templates")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir templates: %w", err)
	}

	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0644)
}
