package appdetect

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/exp/slices"
)

type PythonDetector struct {
}

func (pd *PythonDetector) Type() ProjectType {
	return Python
}

func (pd *PythonDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
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
			for scanner.Scan() {
				split := strings.Split(scanner.Text(), "==")
				if len(split) < 1 {
					continue
				}

				module := strings.TrimSpace(split[0])
				switch module {
				case "flask_mysqldb", "mysqlclient":
					project.Frameworks = append(project.Frameworks, DbMySql)
				case "psycopg2", "psycopg2-binary":
					project.Frameworks = append(project.Frameworks, DbPostgres)
				case "pymongo", "beanie":
					project.Frameworks = append(project.Frameworks, DbMongo)
				}
			}

			slices.SortFunc(project.Frameworks, func(a, b Framework) bool {
				return string(a) < string(b)
			})

			return project, nil
		}
	}

	return nil, nil
}
