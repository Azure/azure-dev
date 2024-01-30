package appdetect

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotNetDetector struct {
	dotnetCli dotnet.DotNetCli
}

func (dd *dotNetDetector) Language() Language {
	return DotNet
}

func (dd *dotNetDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
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
		switch strings.ToLower(name) {
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
		projectPath := filepath.Join(path, projFileName)
		if isWasm, err := dd.isWasmProject(ctx, projectPath); err != nil {
			log.Printf("error checking if %s is a browser-wasm project: %v", projectPath, err)
		} else if isWasm { // Web assembly projects currently not supported as a hosted application project
			return nil, filepath.SkipDir
		}

		return &Project{
			Language:      DotNet,
			Path:          path,
			DetectionRule: "Inferred by presence of: " + fmt.Sprintf("%s, %s", projFileName, startUpFileName),
		}, nil
	}

	return nil, nil
}

func (ad *dotNetDetector) isWasmProject(ctx context.Context, projectPath string) (bool, error) {
	value, err := ad.dotnetCli.GetMsBuildProperty(ctx, projectPath, "RuntimeIdentifier")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(value) == "browser-wasm", nil
}
