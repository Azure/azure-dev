// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Ensure DemoImporterProvider implements ImporterProvider interface
var _ azdext.ImporterProvider = &DemoImporterProvider{}

// DemoImporterProvider is a minimal implementation of ImporterProvider for demonstration.
// It detects projects that contain a "demo.manifest.json" marker file and generates
// simple infrastructure from its contents.
type DemoImporterProvider struct {
	azdClient *azdext.AzdClient
}

// NewDemoImporterProvider creates a new DemoImporterProvider instance
func NewDemoImporterProvider(azdClient *azdext.AzdClient) azdext.ImporterProvider {
	return &DemoImporterProvider{
		azdClient: azdClient,
	}
}

// demoManifest represents the structure of demo.manifest.json
type demoManifest struct {
	Services map[string]demoManifestService `json:"services"`
}

type demoManifestService struct {
	Language string `json:"language"`
	Host     string `json:"host"`
	Path     string `json:"path"`
}

// CanImport checks if the project contains a demo.manifest.json file.
func (p *DemoImporterProvider) CanImport(
	ctx context.Context,
	svcConfig *azdext.ServiceConfig,
) (bool, error) {
	manifestPath := filepath.Join(svcConfig.RelativePath, "demo.manifest.json")
	_, err := os.Stat(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Services extracts services from the demo.manifest.json file.
func (p *DemoImporterProvider) Services(
	ctx context.Context,
	projectConfig *azdext.ProjectConfig,
	svcConfig *azdext.ServiceConfig,
) (map[string]*azdext.ServiceConfig, error) {
	manifest, err := p.readManifest(svcConfig.RelativePath)
	if err != nil {
		return nil, fmt.Errorf("reading demo manifest: %w", err)
	}

	services := make(map[string]*azdext.ServiceConfig, len(manifest.Services))
	for name, svc := range manifest.Services {
		services[name] = &azdext.ServiceConfig{
			Name:         name,
			RelativePath: svc.Path,
			Language:     svc.Language,
			Host:         svc.Host,
		}
	}

	return services, nil
}

// ProjectInfrastructure generates minimal Bicep infrastructure.
func (p *DemoImporterProvider) ProjectInfrastructure(
	ctx context.Context,
	svcConfig *azdext.ServiceConfig,
	progress azdext.ProgressReporter,
) (*azdext.ImporterProjectInfrastructureResponse, error) {
	progress("Generating demo infrastructure...")
	time.Sleep(500 * time.Millisecond)

	mainBicep := `targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment')
param environmentName string

@description('Primary location for all resources')
param location string

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
}

output AZURE_RESOURCE_GROUP string = rg.name
`

	return &azdext.ImporterProjectInfrastructureResponse{
		InfraOptions: &azdext.InfraOptions{
			Provider: "bicep",
			Module:   "main",
		},
		Files: []*azdext.GeneratedFile{
			{
				Path:    "main.bicep",
				Content: []byte(mainBicep),
			},
		},
	}, nil
}

// GenerateAllInfrastructure generates the complete infrastructure filesystem.
func (p *DemoImporterProvider) GenerateAllInfrastructure(
	ctx context.Context,
	projectConfig *azdext.ProjectConfig,
	svcConfig *azdext.ServiceConfig,
) ([]*azdext.GeneratedFile, error) {
	mainBicep := `targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment')
param environmentName string

@description('Primary location for all resources')
param location string

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
}

output AZURE_RESOURCE_GROUP string = rg.name
`

	return []*azdext.GeneratedFile{
		{
			Path:    "infra/main.bicep",
			Content: []byte(mainBicep),
		},
	}, nil
}

func (p *DemoImporterProvider) readManifest(basePath string) (*demoManifest, error) {
	manifestPath := filepath.Join(basePath, "demo.manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", manifestPath, err)
	}

	var manifest demoManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", manifestPath, err)
	}

	return &manifest, nil
}
