package appdetect

import (
	"io/fs"
	"path/filepath"
	"strings"
)

func detectDocker(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "dockerfile" {
			return &Docker{
				Path: filepath.Join(path, entry.Name()),
			}, nil
		}
	}

	return nil, nil
}
