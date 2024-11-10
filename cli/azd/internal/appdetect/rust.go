package appdetect

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type rustDetector struct{}

func (rd *rustDetector) Language() Language {
	return Rust
}

func (rd *rustDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "cargo.toml" {
			project := &Project{
				Language:      Rust,
				Path:          path,
				DetectionRule: "Inferred by presence of: " + entry.Name(),
			}

			contents, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}

			var cargoToml struct {
				Dependencies map[string]toml.Primitive `toml:"dependencies"`
			}
			err = toml.Unmarshal(contents, &cargoToml)
			if err != nil {
				return nil, err
			}

			for k := range cargoToml.Dependencies {
				switch k {
				case string(RsActix):
					project.Dependencies = append(project.Dependencies, RsActix)
				case string(RsAxum):
					project.Dependencies = append(project.Dependencies, RsAxum)
				case string(RsYew):
					project.Dependencies = append(project.Dependencies, RsYew)
				}
			}

			return project, nil
		}
	}

	return nil, nil
}
