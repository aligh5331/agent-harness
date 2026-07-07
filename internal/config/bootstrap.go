package config

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Embedded defaults
// ---------------------------------------------------------------------------

//go:embed embedded/agents/*.md embedded/skills/*/SKILL.md
var embeddedFS embed.FS

const embedSourcePrefix = "embedded"

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

// Bootstrap ensures embedded defaults are extracted to the .aa/ directory
// in targetDir. Safe to call on every startup. Idempotent after first extraction.
//
// Extraction policy:
//   - If .aa/ does not exist       → full extraction of all embedded files
//   - If .aa/ exists, file present → disk content is authoritative (never overwrite)
//   - If .aa/ exists, file missing → re-extract that single file from embedded
func Bootstrap(targetDir string) error {
	aaDir := filepath.Join(targetDir, ".aa")

	_, err := os.Stat(aaDir)
	if err == nil {
		// .aa/ exists — check for missing files only.
		return ensureMissingFiles(embeddedFS, embedSourcePrefix, aaDir)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("config: stat .aa: %w", err)
	}

	// .aa/ does not exist — full extraction.
	return extractAll(embeddedFS, embedSourcePrefix, aaDir)
}

// extractAll performs a full extraction of embedded files to targetDir.
func extractAll(efs embed.FS, sourcePrefix, targetDir string) error {
	return fs.WalkDir(efs, sourcePrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(sourcePrefix, path)
		if err != nil {
			return fmt.Errorf("config: compute rel path: %w", err)
		}
		targetPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := efs.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: read embedded %s: %w", path, err)
		}
		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return fmt.Errorf("config: write %s: %w", targetPath, err)
		}
		return nil
	})
}

// ensureMissingFiles walks the embedded FS and re-extracts any files that
// don't exist on disk. Never overwrites existing files.
func ensureMissingFiles(efs embed.FS, sourcePrefix, aaDir string) error {
	return fs.WalkDir(efs, sourcePrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(sourcePrefix, path)
		if err != nil {
			return fmt.Errorf("config: compute rel path: %w", err)
		}
		targetPath := filepath.Join(aaDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		// Check if the file exists on disk.
		_, err = os.Stat(targetPath)
		if err == nil {
			return nil // file exists — disk wins, do not overwrite
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("config: stat %s: %w", targetPath, err)
		}

		// File missing — re-extract from embedded defaults.
		data, err := efs.ReadFile(path)
		if err != nil {
			return fmt.Errorf("config: read embedded %s: %w", path, err)
		}
		return os.WriteFile(targetPath, data, 0644)
	})
}

// ---------------------------------------------------------------------------
// Skills Manifest
// ---------------------------------------------------------------------------

// ReadSkillsManifest returns a compact string listing all available skills
// (name + description) by reading each SKILL.md frontmatter from .aa/skills/.
// Returns empty string if the skills directory does not exist or has no files.
func ReadSkillsManifest(projectRoot string) (string, error) {
	skillsDir := filepath.Join(projectRoot, ".aa", "skills")

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("config: read skills dir: %w", err)
	}

	var lines []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name, desc := parseSkillManifest(data)
		if name != "" {
			lines = append(lines, fmt.Sprintf("- **%s**: %s", name, desc))
		}
	}

	if len(lines) == 0 {
		return "", nil
	}
	return "Available skills:\n" + strings.Join(lines, "\n"), nil
}

// parseSkillManifest extracts just name and description from SKILL.md
// frontmatter. Returns empty strings on any parse failure (silently ignored).
func parseSkillManifest(data []byte) (name, desc string) {
	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, frontmatterOpen) &&
		!bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
		return "", ""
	}

	openLen := len(frontmatterOpen)
	if bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
		openLen = len(frontmatterOpenCRLF)
	}
	rest := trimmed[openLen:]

	idx := bytes.Index(rest, frontmatterClose)
	if idx < 0 {
		if bytes.HasSuffix(rest, []byte("\n---")) {
			idx = len(rest) - 4
		} else {
			return "", ""
		}
	}

	yamlBlock := rest[:idx]

	var manifest struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(yamlBlock, &manifest); err != nil {
		return "", ""
	}

	// Validate: name and description should be non-empty.
	if manifest.Name == "" || manifest.Description == "" {
		return "", ""
	}

	return manifest.Name, manifest.Description
}
