package appdetect

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type PackagesJson struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type javaScriptDetector struct {
}

func (nd *javaScriptDetector) Language() Language {
	return JavaScript
}

func (nd *javaScriptDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
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

			angularAdded := false
			viteAdded := false
			databaseDepMap := map[DatabaseDep]struct{}{}

			for dep := range packagesJson.Dependencies {
				switch dep {
				case "react":
					project.Dependencies = append(project.Dependencies, JsReact)
				case "jquery":
					project.Dependencies = append(project.Dependencies, JsJQuery)
				case "vite":
					project.Dependencies = append(project.Dependencies, JsVite)
					viteAdded = true
				case "next":
					project.Dependencies = append(project.Dependencies, JsNext)
				default:
					if strings.HasPrefix(dep, "@angular") && !angularAdded {
						project.Dependencies = append(project.Dependencies, JsAngular)
						angularAdded = true
					}
				}

				switch dep {
				case "mysql":
					databaseDepMap[DbMySql] = struct{}{}
				case "mongodb", "mongojs", "mongoose":
					databaseDepMap[DbMongo] = struct{}{}
				case "pg", "pg-promise":
					databaseDepMap[DbPostgres] = struct{}{}
				case "tedious":
					databaseDepMap[DbSqlServer] = struct{}{}
				case "redis", "redis-om":
					databaseDepMap[DbRedis] = struct{}{}
				}
			}

			for dep := range packagesJson.DevDependencies {
				switch dep {
				case "vite":
					if !viteAdded {
						project.Dependencies = append(project.Dependencies, JsVite)
					}
				}
			}

			if len(databaseDepMap) > 0 {
				project.DatabaseDeps = maps.Keys(databaseDepMap)
				slices.SortFunc(project.DatabaseDeps, func(a, b DatabaseDep) bool {
					return string(a) < string(b)
				})
			}

			slices.SortFunc(project.Dependencies, func(a, b Dependency) bool {
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
