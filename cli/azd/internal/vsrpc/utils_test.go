// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/pkg/apphost"
	"github.com/azure/azure-dev/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/pkg/project"
	"github.com/stretchr/testify/require"
)

func Test_azdContext(t *testing.T) {
	root := t.TempDir()
	nearestRel := filepath.Join("nearest", "apphost.csproj")
	nearest := filepath.Join(root, nearestRel)
	inAppHost := filepath.Join(root, filepath.Join("in-apphost", "apphost.csproj"))
	nearestUnmatched := filepath.Join(root, filepath.Join("nearest-unmatched", "apphost.csproj"))

	// Create app host directories and files
	require.NoError(t, createAppHost(nearest))
	require.NoError(t, createAppHost(inAppHost))
	require.NoError(t, createAppHost(nearestUnmatched))

	// By default, no azure.yaml is present. All projects would choose their app host directory as the context directory.
	ctxDir, err := azdContext(nearest)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearest), ctxDir.ProjectDirectory())

	ctxDir, err = azdContext(inAppHost)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(inAppHost), ctxDir.ProjectDirectory())

	ctxDir, err = azdContext(nearestUnmatched)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearestUnmatched), ctxDir.ProjectDirectory())

	// Create azure.yaml files.
	require.NoError(t, createProject(root, nearestRel))
	require.NoError(t, createProject(filepath.Dir(inAppHost), "apphost.csproj"))

	// nearest uses 'root'
	ctxDir, err = azdContext(nearest)
	require.NoError(t, err)
	require.Equal(t, root, ctxDir.ProjectDirectory())

	// inAppHost uses 'in-apphost'
	ctxDir, err = azdContext(inAppHost)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(inAppHost), ctxDir.ProjectDirectory())

	// nearestUnmatched uses its own directory
	ctxDir, err = azdContext(nearestUnmatched)
	require.NoError(t, err)
	require.Equal(t, filepath.Dir(nearestUnmatched), ctxDir.ProjectDirectory())
}

func Test_servicesFromManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest *apphost.Manifest
		expected []*Service
	}{
		{
			name: "empty manifest",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{},
			},
			expected: []*Service{},
		},
		{
			name: "manifest with project resources",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"webapi": {
						Type: "project.v0",
						Path: to.Ptr("./src/WebApi/WebApi.csproj"),
					},
					"frontend": {
						Type: "project.v0",
						Path: to.Ptr("./src/Frontend/Frontend.csproj"),
					},
				},
			},
			expected: []*Service{
				{
					Name: "webapi",
					Path: "./src/WebApi/WebApi.csproj",
				},
				{
					Name: "frontend",
					Path: "./src/Frontend/Frontend.csproj",
				},
			},
		},
		{
			name: "manifest with mixed resource types",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"api": {
						Type: "project.v0",
						Path: to.Ptr("./Api/Api.csproj"),
					},
					"database": {
						Type:  "container.v1",
						Image: to.Ptr("postgres:latest"),
					},
					"worker": {
						Type: "project.v1",
						Path: to.Ptr("./Worker/Worker.csproj"),
					},
				},
			},
			expected: []*Service{
				{
					Name: "api",
					Path: "./Api/Api.csproj",
				},
				{
					Name: "worker",
					Path: "./Worker/Worker.csproj",
				},
			},
		},
		{
			name: "manifest with no project resources",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"redis": {
						Type:  "container.v0",
						Image: to.Ptr("redis:latest"),
					},
					"postgres": {
						Type:  "container.v0",
						Image: to.Ptr("postgres:latest"),
					},
				},
			},
			expected: []*Service{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := servicesFromManifest(tt.manifest)

			require.Len(t, result, len(tt.expected))

			// Convert to maps for easier comparison since order may vary
			resultMap := make(map[string]string)
			for _, svc := range result {
				resultMap[svc.Name] = svc.Path
			}

			expectedMap := make(map[string]string)
			for _, svc := range tt.expected {
				expectedMap[svc.Name] = svc.Path
			}

			require.Equal(t, expectedMap, resultMap)
		})
	}
}
func createProject(prjDir string, appHostPath string) error {
	err := os.MkdirAll(prjDir, 0755)
	if err != nil {
		return err
	}
	prjPath := filepath.Join(prjDir, azdcontext.ProjectFileName)

	prjConfig := &project.ProjectConfig{
		Name: "app",
		Services: map[string]*project.ServiceConfig{
			"app": {
				Host:         project.ContainerAppTarget,
				Language:     project.ServiceLanguageDotNet,
				RelativePath: appHostPath,
			},
		},
	}

	return project.Save(context.Background(), prjConfig, prjPath)
}

func createAppHost(path string) error {
	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
