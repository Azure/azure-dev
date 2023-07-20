package appdetect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/slices"
)

type PackagesJson struct {
	Dependencies map[string]string `json:"dependencies"`
	//DevDependencies map[string]string `json:"devDependencies"`
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

			for dep := range packagesJson.Dependencies {
				switch dep {
				case "react":
					project.Frameworks = append(project.Frameworks, React)
				case "jquery":
					project.Frameworks = append(project.Frameworks, JQuery)
				case "vue":
					project.Frameworks = append(project.Frameworks, VueJs)
				case "mysql":
					project.Frameworks = append(project.Frameworks, DbMySql)
				case "mongodb":
					project.Frameworks = append(project.Frameworks, DbMongo)
				case "pg-promise":
					project.Frameworks = append(project.Frameworks, DbPostgres)
				case "tedious":
					project.Frameworks = append(project.Frameworks, DbSqlServer)
				default:
					if strings.HasPrefix(dep, "@angular") {
						project.Frameworks = append(project.Frameworks, Angular)
					}
				}
			}

			slices.SortFunc(project.Frameworks, func(a, b Framework) bool {
				return string(a) < string(b)
			})

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
