// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// add.go — NewAddCmd
// ---------------------------------------------------------------------------

func TestNewAddCmd_ReturnsCommand(t *testing.T) {
	t.Parallel()
	cmd := NewAddCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "add", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

// ---------------------------------------------------------------------------
// add.go — selectMenu
// ---------------------------------------------------------------------------

func TestSelectMenu_AllNamespacesPresent(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	menu := a.selectMenu()
	require.NotEmpty(t, menu)

	namespaces := make(map[string]bool, len(menu))
	for _, m := range menu {
		namespaces[m.Namespace] = true
		assert.NotEmpty(t, m.Label, "menu item %q should have a label", m.Namespace)
		assert.NotNil(t, m.SelectResource, "menu item %q should have a SelectResource func", m.Namespace)
	}

	for _, ns := range []string{"db", "host", "ai", "messaging", "storage", "keyvault", "existing"} {
		assert.True(t, namespaces[ns], "expected namespace %q in menu", ns)
	}
}

// ---------------------------------------------------------------------------
// add_configure.go — Configure: default (unknown) type
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — Configure: Existing with name preset
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — Configure: DB types with name preset (short-circuit)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — Configure: CosmosDB sets CosmosDBProps
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — Configure: OpenAI with name preset
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — Configure: host types with empty resources
// (fillUses short-circuits when no resources to link)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go / add_configure_messaging.go — duplicate messaging errors
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go / add_configure_storage.go — storage duplicate & invalid props
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure.go — ConfigureLive
// ---------------------------------------------------------------------------

func TestConfigureLive_ExistingResource(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r := &project.ResourceConfig{
		Type:     project.ResourceTypeOpenAiModel,
		Name:     "existing-model",
		Existing: true,
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := a.ConfigureLive(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, r, got)
}

func TestConfigureLive_NonAiType_ReturnsUnchanged(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbPostgres,
		Name: "my-db",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	got, err := a.ConfigureLive(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, r, got)
}

// ---------------------------------------------------------------------------
// add_configure_existing.go — ConfigureExisting with name preset
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure_host.go — PromptPort (pure language-based paths)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_configure_host.go — addServiceAsResource
// ---------------------------------------------------------------------------

func TestAddServiceAsResource_UnsupportedTarget(t *testing.T) {
	t.Parallel()
	svc := &project.ServiceConfig{
		Name: "svc",
		Host: project.ServiceTargetKind("unsupported"),
	}
	_, err := addServiceAsResource(t.Context(), nil, svc, appdetect.Project{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service target")
}

func TestAddServiceAsResource_ContainerApp_NoDockerfile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir() // no Dockerfile inside
	svc := &project.ServiceConfig{
		Name:         "test-svc",
		Host:         project.ContainerAppTarget,
		Language:     project.ServiceLanguagePython,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.Python}
	r, err := addServiceAsResource(t.Context(), nil, svc, prj)
	require.NoError(t, err)
	assert.Equal(t, "test-svc", r.Name)
	assert.Equal(t, project.ResourceTypeHostContainerApp, r.Type)
	props, ok := r.Props.(project.ContainerAppProps)
	require.True(t, ok)
	assert.Equal(t, 80, props.Port)
}

func TestAddServiceAsResource_ContainerApp_JavaPort(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	svc := &project.ServiceConfig{
		Name:         "java-svc",
		Host:         project.ContainerAppTarget,
		Language:     project.ServiceLanguageJava,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.Java}
	r, err := addServiceAsResource(t.Context(), nil, svc, prj)
	require.NoError(t, err)
	props, ok := r.Props.(project.ContainerAppProps)
	require.True(t, ok)
	assert.Equal(t, 8080, props.Port)
}

func TestAddServiceAsResource_ContainerApp_DotNetPort(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	svc := &project.ServiceConfig{
		Name:         "dotnet-svc",
		Host:         project.ContainerAppTarget,
		Language:     project.ServiceLanguageDotNet,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.DotNet}
	r, err := addServiceAsResource(t.Context(), nil, svc, prj)
	require.NoError(t, err)
	props, ok := r.Props.(project.ContainerAppProps)
	require.True(t, ok)
	assert.Equal(t, 8080, props.Port)
}

func TestAddServiceAsResource_AppService_UnsupportedLanguage(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	svc := &project.ServiceConfig{
		Name:         "java-svc",
		Host:         project.AppServiceTarget,
		Language:     project.ServiceLanguageJava,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.Java}
	_, err := addServiceAsResource(t.Context(), nil, svc, prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported language")
}

// ---------------------------------------------------------------------------
// add_configure_host.go — ServiceFromDetect additional cases
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// add_select.go — selectStorage, selectKeyVault
// ---------------------------------------------------------------------------

func TestSelectStorage_ReturnType(t *testing.T) {
	t.Parallel()
	r, err := selectStorage(nil, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeStorage, r.Type)
	_, ok := r.Props.(project.StorageProps)
	assert.True(t, ok, "expected StorageProps")
}

func TestSelectKeyVault_ReturnType(t *testing.T) {
	t.Parallel()
	r, err := selectKeyVault(nil, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeKeyVault, r.Type)
}

// ---------------------------------------------------------------------------
// add_configure.go — promptUsedBy
// ---------------------------------------------------------------------------

func TestPromptUsedBy_EmptyResources(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}
	result, err := promptUsedBy(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestPromptUsedBy_NonHostResources(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"postgres": {Type: project.ResourceTypeDbPostgres, Name: "postgres"},
			},
		},
	}
	result, err := promptUsedBy(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestPromptUsedBy_HostAlreadyUsesResource(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"web": {
					Type: project.ResourceTypeHostContainerApp,
					Name: "web",
					Uses: []string{"redis"},
				},
			},
		},
	}
	result, err := promptUsedBy(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestPromptUsedBy_DifferentHostTypesSkipped(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp,
		Name: "backend",
	}
	opts := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"web": {
					Type: project.ResourceTypeHostAppService, // different host type
					Name: "web",
				},
			},
		},
	}
	result, err := promptUsedBy(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// add_select_ai.go — selectSearch, selectOpenAi, selectAiModel
// ---------------------------------------------------------------------------

func TestSelectSearch_ReturnType(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r, err := a.selectSearch(nil, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeAiSearch, r.Type)
}

func TestSelectOpenAi_ReturnType(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r, err := a.selectOpenAi(nil, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeOpenAiModel, r.Type)
}

func TestSelectAiModel_ReturnType(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r, err := a.selectAiModel(nil, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeAiProject, r.Type)
}

// ---------------------------------------------------------------------------
// add_select_ai.go — selectFromSkus
// ---------------------------------------------------------------------------

func TestSelectFromSkus_Empty(t *testing.T) {
	t.Parallel()
	_, err := selectFromSkus(t.Context(), nil, "Select", []ModelSku{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no skus found")
}

func TestSelectFromSkus_SingleAutoSelects(t *testing.T) {
	t.Parallel()
	expected := ModelSku{
		Name:      "Standard",
		UsageName: "std",
		Capacity:  ModelSkuCapacity{Default: 10},
	}
	got, err := selectFromSkus(t.Context(), nil, "Select", []ModelSku{expected})
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

// ---------------------------------------------------------------------------
// add_select_ai.go — selectFromMap (single-entry auto-select)
// ---------------------------------------------------------------------------

func TestSelectFromMap_SingleEntry(t *testing.T) {
	t.Parallel()
	m := map[string]string{"only-key": "only-value"}
	key, val, err := selectFromMap(t.Context(), nil, "Pick one", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "only-key", key)
	assert.Equal(t, "only-value", val)
}

func TestSelectFromMap_SingleEntry_ComplexType(t *testing.T) {
	t.Parallel()
	m := map[string]ModelCatalogKind{
		"gpt-4o": {Kinds: map[string]ModelCatalogVersions{}},
	}
	key, val, err := selectFromMap(t.Context(), nil, "Select model", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", key)
	assert.NotNil(t, val.Kinds)
}

// ---------------------------------------------------------------------------
// diff.go — DiffBlocks: modified entry (same key, different value)
// ---------------------------------------------------------------------------

func TestDiffBlocks_ModifiedEntry(t *testing.T) {
	t.Parallel()
	old := map[string]*project.ResourceConfig{
		"db": {Type: project.ResourceTypeDbPostgres},
	}
	newMap := map[string]*project.ResourceConfig{
		"db": {Type: project.ResourceTypeDbPostgres, Uses: []string{"web"}},
	}
	result, err := DiffBlocks(old, newMap)
	require.NoError(t, err)
	assert.Contains(t, result, "db:")
	assert.NotEmpty(t, result)
}

// ---------------------------------------------------------------------------
// diff.go — DiffBlocks: multiple new entries (verify sorted output)
// ---------------------------------------------------------------------------

func TestDiffBlocks_MultipleNewEntries_Sorted(t *testing.T) {
	t.Parallel()
	old := map[string]*project.ResourceConfig{}
	newMap := map[string]*project.ResourceConfig{
		"beta":  {Type: project.ResourceTypeDbRedis, Name: "beta"},
		"alpha": {Type: project.ResourceTypeDbPostgres, Name: "alpha"},
	}
	result, err := DiffBlocks(old, newMap)
	require.NoError(t, err)

	alphaIdx := strings.Index(result, "alpha:")
	betaIdx := strings.Index(result, "beta:")
	require.Greater(t, alphaIdx, -1, "expected alpha in output")
	require.Greater(t, betaIdx, -1, "expected beta in output")
	assert.Less(t, alphaIdx, betaIdx, "entries should be sorted alphabetically")
}

// ---------------------------------------------------------------------------
// diff.go — DiffBlocks: new + existing mix
// ---------------------------------------------------------------------------

func TestDiffBlocks_NewAndExistingMix(t *testing.T) {
	t.Parallel()
	existing := &project.ResourceConfig{Type: project.ResourceTypeDbRedis, Name: "redis"}
	old := map[string]*project.ResourceConfig{
		"redis": existing,
	}
	newMap := map[string]*project.ResourceConfig{
		"redis":    existing,                                                 // unchanged
		"postgres": {Type: project.ResourceTypeDbPostgres, Name: "postgres"}, // new
	}
	result, err := DiffBlocks(old, newMap)
	require.NoError(t, err)
	// Unchanged redis should NOT appear, new postgres should appear
	assert.Contains(t, result, "postgres:")
	assert.Contains(t, result, "+")
}

// ---------------------------------------------------------------------------
// add_preview.go — previewWriter edge cases
// ---------------------------------------------------------------------------

func TestPreviewWriter_EmptyWrite(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := &previewWriter{w: &buf}
	n, err := pw.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, buf.String())
}

func TestPreviewWriter_NoNewline_BuffersInternally(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := &previewWriter{w: &buf}
	n, err := pw.Write([]byte("partial"))
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	// No newline means nothing flushed to underlying writer
	assert.Empty(t, buf.String())
}

func TestPreviewWriter_MultipleLines(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := &previewWriter{w: &buf}
	input := "+  added\n   normal\n"
	n, err := pw.Write([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, len(input), n)
	out := buf.String()
	assert.Contains(t, out, "added")
	assert.Contains(t, out, "normal")
}
