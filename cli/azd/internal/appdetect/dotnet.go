package appdetect

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

type DotNetDetector struct {
}

func (dd *DotNetDetector) Type() ProjectType {
	return DotNet
}

func (dd *DotNetDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	var hasProjectFile bool
	var hasStartupFile bool

	var projFileName string
	var startUpFileName string

	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)
		if name == "Program.cs" || name == "Program.vb" || name == "Program.fs" {
			hasStartupFile = true
			projFileName = name
		} else if ext == ".csproj" || ext == ".fsproj" || ext == ".vbproj" {
			hasProjectFile = true
			startUpFileName = name
		}
	}

	if hasProjectFile && hasStartupFile {
		return &Project{
			Language:      DotNet,
			Path:          path,
			DetectionRule: "Inferred by presence of: " + fmt.Sprintf("%s, %s", projFileName, startUpFileName),
		}, nil
	}

	return nil, nil
}
