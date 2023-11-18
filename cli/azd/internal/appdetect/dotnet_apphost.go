package appdetect

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotNetAppHostDetector struct {
	dotnetCli dotnet.DotNetCli
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
			if isAppHost, err := ad.isAppHostProject(ctx, filepath.Join(projectPath)); err != nil {
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

// isAppHostProject returns true if the project at the given path has an MS Build Property named "IsAspireHost" which is
// set to "true".
func (ad *dotNetAppHostDetector) isAppHostProject(ctx context.Context, projectPath string) (bool, error) {
	value, err := ad.dotnetCli.GetMsBuildProperty(ctx, projectPath, "IsAspireHost")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(value) == "true", nil
}
