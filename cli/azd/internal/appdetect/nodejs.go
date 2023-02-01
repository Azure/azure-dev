package appdetect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/maps"
)

type PackagesJson struct {
	Dependencies map[string]string `json:"dependencies"`
	//DevDependencies map[string]string `json:"devDependencies"`
}

type NodeJsDetector struct {
}

func (nd *NodeJsDetector) DisplayName() string {
	return "nodejs"
}

func (nd *NodeJsDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if entry.Name() == "package.json" {
			project := &Project{
				Language:  string(NodeJs),
				Path:      path,
				InferRule: "Inferred by presence of: " + entry.Name(),
			}

			contents, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}

			var packagesJson PackagesJson
			err = json.Unmarshal(contents, &packagesJson)
			if err == nil {
				frameworks := map[Framework]struct{}{}
				for _, dep := range packagesJson.Dependencies {
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

				project.Frameworks = maps.Keys(frameworks)
			}

			return project, nil
		}
	}

	return nil, nil
}
