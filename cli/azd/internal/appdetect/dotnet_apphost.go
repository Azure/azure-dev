package appdetect

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotNetAppHostDetector struct {
	dotnetCli *dotnet.Cli
}

func (ad *dotNetAppHostDetector) Language() Language {
	return DotNetAppHost
}

func (ad *dotNetAppHostDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		name := entry.Name()
		ext := filepath.Ext(name)

		switch ext {
		case ".csproj", ".fsproj", ".vbproj":
			projectPath := filepath.Join(path, name)
			if isAppHost, err := ad.dotnetCli.IsAspireHostProject(ctx, filepath.Join(projectPath)); err != nil {
				log.Printf("error checking if %s is an app host project: %v", projectPath, err)
			} else if isAppHost {
				return &Project{
					Language:      DotNetAppHost,
					Path:          projectPath,
					DetectionRule: "Inferred by presence of: " + projectPath,
				}, nil
			}
		}
	}

	return nil, nil
}
