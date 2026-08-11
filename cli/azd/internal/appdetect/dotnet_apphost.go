// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"go.opentelemetry.io/otel/trace"
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

	// Finally, check for an Aspire polyglot (non-C#) AppHost (e.g. TypeScript or Python). azd does
	// not support these yet, so surface an actionable error instead of letting the AppHost fall
	// through to a generic source build (which produces confusing Docker/buildpack failures).
	// See https://github.com/Azure/azure-dev/issues/7138.
	if language, appHostFile, ok := detectAspirePolyglotAppHost(path, entries); ok {
		_, span := tracing.Start(
			ctx,
			events.AspireUnsupportedAppHostEvent,
			trace.WithAttributes(fields.AspireAppHostLanguageKey.String(language)))
		span.End()

		return nil, &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"detected an Aspire polyglot (%s) AppHost at %q, which azd does not support yet",
				language, appHostFile),
			Message: "azd does not yet support Aspire polyglot (non-C#) AppHosts, " +
				"such as TypeScript or Python AppHosts.",
			Suggestion: "Track and upvote support for this scenario at " +
				"https://github.com/Azure/azure-dev/issues/7138.\n" +
				"In the meantime, use a C# (.NET) Aspire AppHost, or publish with the Aspire CLI " +
				"(for example, 'aspire deploy').",
			Links: []errorhandler.ErrorLink{
				{
					URL:   "https://github.com/Azure/azure-dev/issues/7138",
					Title: "Support Aspire polyglot (non-C#) AppHost projects in azd",
				},
			},
		}
	}

	return nil, nil
}
