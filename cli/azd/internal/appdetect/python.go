// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"bufio"
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type pythonDetector struct {
}

func (pd *pythonDetector) Language() Language {
	return Python
}

func (pd *pythonDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	// Check for pyproject.toml first (modern Python), then requirements.txt
	var depFile string
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if name == "pyproject.toml" {
			depFile = entry.Name()
			break
		}
		if name == "requirements.txt" {
			depFile = entry.Name()
			// Don't break — keep looking for pyproject.toml
		}
	}

	if depFile == "" {
		return nil, nil
	}

	project := &Project{
		Language:      Python,
		Path:          path,
		DetectionRule: "Inferred by presence of: " + depFile,
	}

	file, err := os.Open(filepath.Join(path, depFile))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	databaseDepMap := map[DatabaseDep]struct{}{}

	for scanner.Scan() {
		line := scanner.Text()
		// For requirements.txt: "package==version"
		// For pyproject.toml: '"package>=version"' in dependencies list
		split := strings.Split(line, "==")
		if strings.TrimSpace(split[0]) == "" {
			continue
		}

		// Normalize: strip quotes, version specifiers, whitespace
		module := strings.ToLower(strings.TrimSpace(split[0]))
		module = strings.Trim(module, "\"' ,")
		// Strip version specifiers for pyproject.toml format
		for _, sep := range []string{">=", "<=", "~=", "!="} {
			if idx := strings.Index(module, sep); idx > 0 {
				module = strings.TrimSpace(module[:idx])
			}
		}

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

	if len(databaseDepMap) > 0 {
		project.DatabaseDeps = slices.SortedFunc(maps.Keys(databaseDepMap),
			func(a, b DatabaseDep) int {
				return strings.Compare(string(a), string(b))
			})
	}

	slices.SortFunc(project.Dependencies, func(a, b Dependency) int {
		return strings.Compare(string(a), string(b))
	})

	return project, nil
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
