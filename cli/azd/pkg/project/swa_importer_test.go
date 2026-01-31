// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSwaImporter_CanImport(t *testing.T) {
	tests := []struct {
		name           string
		services       map[string]*ServiceConfig
		createInfraDir bool
		expected       bool
	}{
		{
			name: "SWA only service without infra",
			services: map[string]*ServiceConfig{
				"web": {
					Host: StaticWebAppTarget,
				},
			},
			createInfraDir: false,
			expected:       true,
		},
		{
			name: "Multiple SWA services without infra",
			services: map[string]*ServiceConfig{
				"frontend": {
					Host: StaticWebAppTarget,
				},
				"admin": {
					Host: StaticWebAppTarget,
				},
			},
			createInfraDir: false,
			expected:       true,
		},
		{
			name: "SWA service with infra folder",
			services: map[string]*ServiceConfig{
				"web": {
					Host: StaticWebAppTarget,
				},
			},
			createInfraDir: true,
			expected:       false,
		},
		{
			name: "Mixed services (SWA + ContainerApp)",
			services: map[string]*ServiceConfig{
				"web": {
					Host: StaticWebAppTarget,
				},
				"api": {
					Host: ContainerAppTarget,
				},
			},
			createInfraDir: false,
			expected:       false,
		},
		{
			name:           "No services",
			services:       map[string]*ServiceConfig{},
			createInfraDir: false,
			expected:       false,
		},
		{
			name: "Non-SWA service",
			services: map[string]*ServiceConfig{
				"api": {
					Host: AppServiceTarget,
				},
			},
			createInfraDir: false,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tempDir := t.TempDir()

			if tt.createInfraDir {
				infraDir := filepath.Join(tempDir, "infra")
				err := os.MkdirAll(infraDir, 0755)
				require.NoError(t, err)
				// Create a dummy file in infra dir to indicate it has content
				err = os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("// bicep"), 0600)
				require.NoError(t, err)
			}

			projectConfig := &ProjectConfig{
				Path:     tempDir,
				Services: tt.services,
			}

			importer := NewSwaImporter()
			result := importer.CanImport(context.Background(), projectConfig)

			require.Equal(t, tt.expected, result)
		})
	}
}

func TestSwaImporter_ProjectInfrastructure(t *testing.T) {
	tempDir := t.TempDir()

	projectConfig := &ProjectConfig{
		Path: tempDir,
		Services: map[string]*ServiceConfig{
			"frontend": {
				Host: StaticWebAppTarget,
			},
		},
	}

	importer := NewSwaImporter()
	infra, err := importer.ProjectInfrastructure(context.Background(), projectConfig)

	require.NoError(t, err)
	require.NotNil(t, infra)
	require.NotEmpty(t, infra.Options.Path)
	require.Equal(t, "main", infra.Options.Module)

	// Verify the generated infra directory exists
	_, err = os.Stat(infra.Options.Path)
	require.NoError(t, err)

	// Verify main.bicep was created
	mainBicepPath := filepath.Join(infra.Options.Path, "main.bicep")
	_, err = os.Stat(mainBicepPath)
	require.NoError(t, err)

	// Verify resources.bicep was created
	resourcesBicepPath := filepath.Join(infra.Options.Path, "resources.bicep")
	_, err = os.Stat(resourcesBicepPath)
	require.NoError(t, err)

	// Cleanup
	err = infra.Cleanup()
	require.NoError(t, err)
}

func TestSwaImporter_GenerateAllInfrastructure(t *testing.T) {
	tempDir := t.TempDir()

	projectConfig := &ProjectConfig{
		Path: tempDir,
		Services: map[string]*ServiceConfig{
			"myapp": {
				Host: StaticWebAppTarget,
			},
		},
	}

	importer := NewSwaImporter()
	fs, err := importer.GenerateAllInfrastructure(context.Background(), projectConfig)

	require.NoError(t, err)
	require.NotNil(t, fs)

	// Verify main.bicep exists in the generated fs
	mainBicep, err := fs.Open("infra/main.bicep")
	require.NoError(t, err)
	mainBicep.Close()

	// Verify resources.bicep exists in the generated fs
	resourcesBicep, err := fs.Open("infra/resources.bicep")
	require.NoError(t, err)
	resourcesBicep.Close()
}
