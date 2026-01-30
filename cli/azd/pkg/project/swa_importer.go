// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/psanford/memfs"
)

// SwaImporter is an importer that handles Static Web App services without explicit infrastructure.
// It generates opinionated infrastructure for SWA-only projects.
type SwaImporter struct{}

// NewSwaImporter creates a new instance of the SWA importer.
func NewSwaImporter() *SwaImporter {
	return &SwaImporter{}
}

// CanImport returns true when the project contains only SWA services and no infra folder exists.
// This allows azd to auto-generate infrastructure for simple SWA projects.
func (si *SwaImporter) CanImport(ctx context.Context, projectConfig *ProjectConfig) bool {
	// Must have at least one service
	if len(projectConfig.Services) == 0 {
		return false
	}

	// All services must target Static Web App
	for _, svc := range projectConfig.Services {
		if svc.Host != StaticWebAppTarget {
			return false
		}
	}

	// Check if infra folder exists
	infraPath := filepath.Join(projectConfig.Path, DefaultProvisioningOptions.Path)
	if projectConfig.Infra.Path != "" {
		infraPath = projectConfig.Infra.Path
		if !filepath.IsAbs(infraPath) {
			infraPath = filepath.Join(projectConfig.Path, infraPath)
		}
	}

	// If infra directory exists and has files, don't use SWA importer
	if files, err := os.ReadDir(infraPath); err == nil && len(files) > 0 {
		return false
	}

	log.Printf("SWA importer: project contains only SWA services and no infra folder")
	return true
}

// ProjectInfrastructure returns the infrastructure configuration for SWA-only projects.
// It generates temporary infrastructure files using scaffold templates.
func (si *SwaImporter) ProjectInfrastructure(
	ctx context.Context,
	projectConfig *ProjectConfig,
) (*Infra, error) {
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	infraFS, err := si.generateInfrastructureFs(ctx, projectConfig)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}

	err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		target := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		contents, err := fs.ReadFile(infraFS, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, contents, osutil.PermissionFile)
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("writing infrastructure: %w", err)
	}

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultProvisioningOptions.Module,
		},
		cleanupDir: tmpDir,
	}, nil
}

// GenerateAllInfrastructure returns a filesystem containing all infrastructure for the SWA project,
// rooted at the project directory (under infra/).
func (si *SwaImporter) GenerateAllInfrastructure(
	ctx context.Context,
	projectConfig *ProjectConfig,
) (fs.FS, error) {
	infraFS, err := si.generateInfrastructureFs(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	infraPathPrefix := DefaultProvisioningOptions.Path
	if projectConfig.Infra.Path != "" {
		infraPathPrefix = projectConfig.Infra.Path
	}

	// root the generated content at the project directory
	generatedFS := memfs.New()
	err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		err = generatedFS.MkdirAll(filepath.Join(infraPathPrefix, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		contents, err := fs.ReadFile(infraFS, path)
		if err != nil {
			return err
		}

		return generatedFS.WriteFile(filepath.Join(infraPathPrefix, path), contents, d.Type().Perm())
	})
	if err != nil {
		return nil, err
	}

	return generatedFS, nil
}

// generateInfrastructureFs generates the infrastructure files for SWA services.
func (si *SwaImporter) generateInfrastructureFs(
	ctx context.Context,
	projectConfig *ProjectConfig,
) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := si.buildInfraSpec(projectConfig)
	if err != nil {
		return nil, fmt.Errorf("building infrastructure spec: %w", err)
	}

	files, err := scaffold.ExecInfraFs(t, *infraSpec)
	if err != nil {
		return nil, fmt.Errorf("executing scaffold templates: %w", err)
	}

	return files, nil
}

// buildInfraSpec builds the infrastructure specification for SWA services.
func (si *SwaImporter) buildInfraSpec(projectConfig *ProjectConfig) (*scaffold.InfraSpec, error) {
	infraSpec := &scaffold.InfraSpec{
		Services: []scaffold.ServiceSpec{},
	}

	for name := range projectConfig.Services {
		svcSpec := scaffold.ServiceSpec{
			Name: name,
			Port: -1, // SWA doesn't use ports in the same way
			Env:  map[string]string{},
			Host: scaffold.StaticWebAppKind,
		}

		// Note: Environment variables for SWA services are handled during deployment via swa-cli,
		// not through infrastructure provisioning. The env map is kept empty here.

		infraSpec.Services = append(infraSpec.Services, svcSpec)
	}

	return infraSpec, nil
}
