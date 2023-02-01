package appdetect

import (
	"io/fs"
	"path/filepath"
)

func DetectDockerProject(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if entry.Name() == "Dockerfile" {
			return &Docker{
				Path: filepath.Join(path, entry.Name()),
			}, nil
		}
	}

	return nil, nil
}
