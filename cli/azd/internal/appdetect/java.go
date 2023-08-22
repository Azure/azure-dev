package appdetect

import (
	"io/fs"
	"strings"
)

type JavaDetector struct {
}

func (jd *JavaDetector) Language() Language {
	return Java
}

func (jd *JavaDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			return &Project{
				Language:      Java,
				Path:          path,
				DetectionRule: "Inferred by presence of: " + entry.Name(),
			}, nil
		}
	}

	return nil, nil
}
