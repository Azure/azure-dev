// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestConfigure_DefaultType_ReturnsUnchanged(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceType("unknown.something"),
		Name: "thing",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := Configure(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, r, got)
}

func TestConfigure_ExistingWithNamePreset(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:     project.ResourceTypeDbPostgres,
		Name:     "existing-db",
		Existing: true,
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := Configure(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "existing-db", got.Name)
	assert.True(t, got.Existing)
}

func TestConfigure_DbTypesWithNamePreset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		resType project.ResourceType
	}{
		{"postgres", project.ResourceTypeDbPostgres},
		{"mysql", project.ResourceTypeDbMySql},
		{"mongo", project.ResourceTypeDbMongo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &project.ResourceConfig{
				Type: tt.resType,
				Name: "my-db",
			}
			opts := PromptOptions{
				PrjConfig: &project.ProjectConfig{
					Resources: map[string]*project.ResourceConfig{},
				},
			}
			got, err := Configure(t.Context(), r, nil, opts)
			require.NoError(t, err)
			assert.Equal(t, "my-db", got.Name)
		})
	}
}

func TestConfigure_CosmosDbWithNamePreset(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbCosmos,
		Name: "my-cosmos",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := Configure(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "my-cosmos", got.Name)
	_, ok := got.Props.(project.CosmosDBProps)
	assert.True(t, ok, "expected CosmosDBProps to be set")
}

func TestConfigure_OpenAiWithNamePreset(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeOpenAiModel,
		Name: "my-model",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := Configure(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "my-model", got.Name)
}

func TestConfigure_HostTypes_EmptyResources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		resType project.ResourceType
	}{
		{"container app", project.ResourceTypeHostContainerApp},
		{"app service", project.ResourceTypeHostAppService},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &project.ResourceConfig{
				Type: tt.resType,
				Name: "my-host",
			}
			opts := PromptOptions{
				PrjConfig: &project.ProjectConfig{
					Resources: map[string]*project.ResourceConfig{},
				},
			}
			got, err := Configure(t.Context(), r, nil, opts)
			require.NoError(t, err)
			assert.Equal(t, "my-host", got.Name)
		})
	}
}

func TestConfigure_MessagingDuplicates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		resType     project.ResourceType
		existingKey string
		wantError   string
	}{
		{
			name:        "event hubs duplicate",
			resType:     project.ResourceTypeMessagingEventHubs,
			existingKey: "event-hubs",
			wantError:   "only one event hubs",
		},
		{
			name:        "service bus duplicate",
			resType:     project.ResourceTypeMessagingServiceBus,
			existingKey: "service-bus",
			wantError:   "only one service bus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &project.ResourceConfig{Type: tt.resType}
			opts := PromptOptions{
				PrjConfig: &project.ProjectConfig{
					Resources: map[string]*project.ResourceConfig{
						tt.existingKey: {},
					},
				},
			}
			_, err := Configure(t.Context(), r, nil, opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestConfigure_StorageDuplicate(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: project.StorageProps{},
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"storage": {},
			},
		},
	}
	_, err := Configure(t.Context(), r, nil, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one Storage")
}

func TestConfigure_StorageInvalidProps(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: nil, // not StorageProps
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	_, err := Configure(t.Context(), r, nil, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource properties")
}

func TestConfigureExisting_WithNamePreset(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:       project.ResourceTypeDbRedis,
		Name:       "my-redis",
		ResourceId: "/subscriptions/sub1/resourceGroups/rg/providers/Microsoft.Cache/redis/my-redis",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := ConfigureExisting(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "my-redis", got.Name)
	assert.Equal(t, r.ResourceId, got.ResourceId)
}

func TestPromptPort_NoDocker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lang     appdetect.Language
		wantPort int
	}{
		{"python returns 80", appdetect.Python, 80},
		{"java returns 8080", appdetect.Java, 8080},
		{"dotnet returns 8080", appdetect.DotNet, 8080},
		{"javascript returns 80", appdetect.JavaScript, 80},
		{"typescript returns 80", appdetect.TypeScript, 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prj := appdetect.Project{
				Language: tt.lang,
				Docker:   nil,
			}
			port, err := PromptPort(nil, t.Context(), "svc", prj)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

func TestPromptPort_DockerEmptyPath(t *testing.T) {
	t.Parallel()
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker:   &appdetect.Docker{Path: ""},
	}
	port, err := PromptPort(nil, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 80, port)
}

func TestPromptPort_SingleDockerPort(t *testing.T) {
	t.Parallel()
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/some/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}},
		},
	}
	port, err := PromptPort(nil, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 3000, port)
}

func TestServiceFromDetect_AngularDependency(t *testing.T) {
	t.Parallel()
	svc, err := ServiceFromDetect(
		"/projects",
		"angular-app",
		appdetect.Project{
			Path:     "/projects/angular-app",
			Language: appdetect.TypeScript,
			Dependencies: []appdetect.Dependency{
				appdetect.JsAngular,
			},
		},
		project.ContainerAppTarget,
	)
	require.NoError(t, err)
	assert.Equal(t, "dist/angular-app", svc.OutputPath)
}

func TestServiceFromDetect_DockerRelativePath(t *testing.T) {
	t.Parallel()
	svc, err := ServiceFromDetect(
		"/projects",
		"docker-svc",
		appdetect.Project{
			Path:     "/projects/app",
			Language: appdetect.Python,
			Docker: &appdetect.Docker{
				Path: "/projects/app/Dockerfile",
			},
		},
		project.ContainerAppTarget,
	)
	require.NoError(t, err)
	assert.Equal(t, "docker-svc", svc.Name)
	assert.Equal(t, "Dockerfile", svc.Docker.Path)
}

func TestServiceFromDetect_WithRootPath(t *testing.T) {
	t.Parallel()
	svc, err := ServiceFromDetect(
		"/projects",
		"mono-svc",
		appdetect.Project{
			Path:     "/projects/app",
			Language: appdetect.Python,
			Docker: &appdetect.Docker{
				Path: "/projects/app/Dockerfile",
			},
			RootPath: "/projects",
		},
		project.ContainerAppTarget,
	)
	require.NoError(t, err)
	assert.Equal(t, "..", svc.Docker.Context)
}

func TestServiceFromDetect_ViteOverridesReact(t *testing.T) {
	t.Parallel()
	svc, err := ServiceFromDetect(
		"/projects",
		"spa",
		appdetect.Project{
			Path:     "/projects/spa",
			Language: appdetect.TypeScript,
			Dependencies: []appdetect.Dependency{
				appdetect.JsReact, // react sets "build"
				appdetect.JsVite,  // vite overrides to "dist"
			},
		},
		project.ContainerAppTarget,
	)
	require.NoError(t, err)
	assert.Equal(t, "dist", svc.OutputPath)
}
