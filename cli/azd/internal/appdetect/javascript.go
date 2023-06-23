package appdetect

import (
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type PackagesJson struct {
	Dependencies map[string]string `json:"dependencies"`
}

type JavaScriptDetector struct {
}

func (nd *JavaScriptDetector) Type() ProjectType {
	return JavaScript
}

func (nd *JavaScriptDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "package.json" {
			project := &Project{
				Language:      JavaScript,
				Path:          path,
				DetectionRule: "Inferred by presence of: " + entry.Name(),
			}

			contents, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}

			var packagesJson PackagesJson
			err = json.Unmarshal(contents, &packagesJson)
			if err != nil {
				return nil, err
			}

			frameworks := map[Framework]struct{}{}
			for dep := range packagesJson.Dependencies {
				if dep == "react" {
					frameworks[React] = struct{}{}
				} else if dep == "jquery" {
					frameworks[JQuery] = struct{}{}
				} else if strings.HasPrefix(dep, "@angular") {
					frameworks[Angular] = struct{}{}
				} else if dep == "vue" {
					frameworks[VueJs] = struct{}{}
				}
			}

			if len(frameworks) > 0 {
				project.Frameworks = maps.Keys(frameworks)
				slices.SortFunc(project.Frameworks, func(a, b Framework) bool {
					return string(a) < string(b)
				})
				log.Printf("Frameworks found: %v\n", project.Frameworks)
			}

			tsFiles := 0
			jsFiles := 0
			err = filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
				if d.IsDir() && d.Name() == "node_modules" {
					return filepath.SkipDir
				}

				if !d.IsDir() {
					switch filepath.Ext(path) {
					case ".js":
						jsFiles++
					case ".ts":
						tsFiles++
					}
				}

				return nil
			})

			if err != nil {
				return nil, err
			}

			if tsFiles > jsFiles {
				project.Language = TypeScript
			}

			return project, nil
		}
	}

	return nil, nil
}
