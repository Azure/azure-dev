package appdetect

import (
	"io/fs"
)

type PythonDetector struct {
}

func (pd *PythonDetector) Type() ProjectType {
	return Python
}

func (pd *PythonDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		// entry.Name() == "pyproject.toml" when azd supports pyproject files
		if entry.Name() == "requirements.txt" {
			return &Project{
				Language:      Python,
				Path:          path,
				DetectionRule: "Inferred by presence of: " + entry.Name(),
			}, nil
		}
	}

	return nil, nil
}
