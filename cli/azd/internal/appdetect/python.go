package appdetect

import (
	"bufio"
	"context"
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

func (pd *pythonDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
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
				case "flask_mysqldb",
					"mysqlclient",
					"aiomysql",
					"asyncmy":
					databaseDepMap[DbMySql] = struct{}{}
				case "psycopg2",
					"psycopg2-binary",
					"psycopg",
					"psycopgbinary",
					"asyncpg",
					"aiopg":
					databaseDepMap[DbPostgres] = struct{}{}
				case "pymongo",
					"beanie",
					"motor":
					databaseDepMap[DbMongo] = struct{}{}
				case "redis", "redis-om":
					databaseDepMap[DbRedis] = struct{}{}
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

// PyFastApiLaunch returns the launch argument for a python FastAPI project to be served by a python web server.
// An empty string is returned if the project is not a FastAPI project.
// An error is returned only if the project path cannot be walked.
func PyFastApiLaunch(projectPath string) (string, error) {
	maxDepth := 2
	// A launch path that looks like: dir1.dir2.main:app
	launchPath := ""

	err := filepath.WalkDir(projectPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(projectPath, path)
		if err != nil {
			return err
		}

		if d.IsDir() && strings.Count(rel, string(os.PathSeparator)) > maxDepth-1 {
			return filepath.SkipDir
		}

		if strings.HasSuffix(path, "main.py") || strings.HasSuffix(path, "app.py") {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				// match on something that looks like:
				//   app = FastAPI()
				//   app = fastapi.FastAPI()
				//   app = FastAPI(
				if strings.Contains(scanner.Text(), "FastAPI(") {
					decl := strings.Split(scanner.Text(), " ")

					if len(decl) >= 3 && decl[1] == "=" {
						mainObjName := decl[0]
						mainFilePath := rel
						// dir1/dir2/main.py -> dir1.dir2.main
						mainPath := strings.ReplaceAll(
							strings.TrimSuffix(mainFilePath, ".py"),
							string(os.PathSeparator),
							".")

						launchPath = mainPath + ":" + mainObjName

						return filepath.SkipAll
					}
				}
			}

			return scanner.Err()
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	return launchPath, nil
}
