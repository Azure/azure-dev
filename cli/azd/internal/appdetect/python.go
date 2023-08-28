package appdetect

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type pythonDetector struct {
}

func (pd *pythonDetector) Language() Language {
	return Python
}

func (pd *pythonDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		// entry.Name() == "pyproject.toml" when azd supports pyproject files
		if strings.ToLower(entry.Name()) == "requirements.txt" {
			project := &Project{
				Language:      Python,
				Path:          path,
				DetectionRule: "Inferred by presence of: " + entry.Name(),
			}

			file, err := os.Open(filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}

			scanner := bufio.NewScanner(file)
			databaseDepMap := map[DatabaseDep]struct{}{}

			for scanner.Scan() {
				split := strings.Split(scanner.Text(), "==")
				if len(split) < 1 {
					continue
				}

				// pip is case insensitive: PEP 426
				// https://peps.python.org/pep-0426/#name
				module := strings.ToLower(strings.TrimSpace(split[0]))
				switch module {
				case "fastapi":
					project.Dependencies = append(project.Dependencies, PyFastApi)
				case "flask":
					project.Dependencies = append(project.Dependencies, PyFlask)
				case "django":
					project.Dependencies = append(project.Dependencies, PyDjango)
				}

				switch module {
				case "flask_mysqldb", "mysqlclient":
					databaseDepMap[DbMySql] = struct{}{}
				case "psycopg2", "psycopg2-binary":
					databaseDepMap[DbPostgres] = struct{}{}
				case "pymongo", "beanie":
					databaseDepMap[DbMongo] = struct{}{}
				}
			}

			if err := file.Close(); err != nil {
				return nil, err
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

			return project, nil
		}
	}

	return nil, nil
}
