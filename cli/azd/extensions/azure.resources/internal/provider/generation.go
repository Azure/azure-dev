// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provider

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"azure.resources/internal/resources"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

// GenerationProvider writes resource sources and returns their Bicep infrastructure entry.
type GenerationProvider struct {
	mu sync.Mutex // serializes exports and their overwrite checks
}

// NewGenerationProvider creates an exporter with no provisioning or Azure dependencies.
func NewGenerationProvider() *GenerationProvider {
	return &GenerationProvider{}
}

// Generate writes resource Bicep sources without changing project or environment configuration.
func (g *GenerationProvider) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if request == nil || request.Entry == nil || request.Options == nil {
		return nil, fmt.Errorf("resources generation requires an entry and non-nil options")
	}

	entry := request.Entry
	if entry.Provider != resources.ProviderName || request.ProjectPath == "" {
		return nil, fmt.Errorf("resources generation requires a resources entry and project directory")
	}
	if err := resources.ValidateEntryName(entry.Name); err != nil {
		return nil, err
	}
	if !validGenerationPath(request.OutputPath) {
		return nil, fmt.Errorf("resources generation requires a project-relative output directory")
	}
	if entry.Config == nil {
		return nil, fmt.Errorf("resources generation requires config")
	}

	files, err := resources.GenerateFromConfig(ctx, resources.GenerateRequest{
		Name: entry.Name, Config: entry.Config.AsMap(),
	})
	if err != nil {
		return nil, err
	}

	if err := writeGeneratedFiles(ctx, request.ProjectPath, request.OutputPath, files, request.Options.Force); err != nil {
		return nil, err
	}

	return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
		Name: entry.Name, Provider: "bicep", Path: request.OutputPath, Module: "main",
	}}, nil
}

// writeGeneratedFiles writes files under the validated outputPath in projectPath.
// It checks every destination before writing and overwrites files only when force is true.
func writeGeneratedFiles(
	ctx context.Context, projectPath, outputPath string, files map[string][]byte, force bool,
) error {
	root, err := os.OpenRoot(projectPath)
	if err != nil {
		return fmt.Errorf("opening generation destination: %w", err)
	}
	defer root.Close()

	names := slices.Sorted(maps.Keys(files))
	for _, name := range names {
		if !validGenerationPath(name) {
			return fmt.Errorf("resource generator returned invalid file path %q", name)
		}

		target := path.Join(outputPath, name)
		info, err := root.Stat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("checking generation destination %q: %w", target, err)
		}
		if info.IsDir() {
			return fmt.Errorf("generation destination %q is a directory", target)
		}
		if !force {
			return fmt.Errorf("generated file %q already exists; use --force to overwrite it", target)
		}
	}

	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}

		target := path.Join(outputPath, name)
		if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating generation directory: %w", err)
		}

		file, err := root.OpenFile(target, flags, 0o644)
		if err != nil {
			return fmt.Errorf("opening generated file %q: %w", target, err)
		}

		_, writeErr := file.Write(files[name])
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return fmt.Errorf("writing generated file %q: %w", target, err)
		}
	}

	return nil
}

func validGenerationPath(value string) bool {
	return fs.ValidPath(value) && value != "." && filepath.IsLocal(value) && !strings.Contains(value, "\\")
}
