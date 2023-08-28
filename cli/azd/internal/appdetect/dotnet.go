package appdetect

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type dotNetDetector struct {
}

func (dd *dotNetDetector) Language() Language {
	return DotNet
}

func (dd *dotNetDetector) DetectProject(path string, entries []fs.DirEntry) (*Project, error) {
	var hasProjectFile bool
	var hasStartupFile bool
	var projFileName string
	var startUpFileName string

	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)

		// This detection logic doesn't work if Program.cs has been renamed, or moved into a different directory.
		// A true detection of an "Application" is much harder since ASP .NET applications are just libraries
		// that are ran with "dotnet run".
		name = strings.ToLower(name)
		switch name {
		case "program.cs", "program.fs", "program.vb":
			hasStartupFile = true
			startUpFileName = name
		}

		switch ext {
		case ".csproj", ".fsproj", ".vbproj":
			hasProjectFile = true
			projFileName = name
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
