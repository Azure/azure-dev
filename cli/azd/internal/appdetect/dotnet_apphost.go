// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotNetAppHostDetector struct {
	dotnetCli *dotnet.Cli
}

func (ad *dotNetAppHostDetector) Language() Language {
	return DotNetAppHost
}

func (ad *dotNetAppHostDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	// First, check for single-file apphost by filename (apphost.cs, case-insensitive)
	// This is more efficient than checking every .cs file
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), dotnet.SingleFileAspireHostName) {
			filePath := filepath.Join(path, entry.Name())
			if isSingleFileAppHost, err := ad.dotnetCli.IsSingleFileAspireHost(filePath); err != nil {
				log.Printf("error checking if %s is a single-file app host: %v", filePath, err)
			} else if isSingleFileAppHost {
				return &Project{
					Language:      DotNetAppHost,
					Path:          filePath,
					DetectionRule: "Inferred by single-file Aspire AppHost: " + filePath,
				}, nil
			}
		}
	}

	// Then, check for project-based apphost (.csproj, .fsproj, .vbproj)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := filepath.Ext(entry.Name())
		if slices.Contains(dotnet.DotNetProjectExtensions, ext) {
			projectPath := filepath.Join(path, entry.Name())
			if isAppHost, err := ad.dotnetCli.IsAspireHostProject(ctx, projectPath); err != nil {
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
