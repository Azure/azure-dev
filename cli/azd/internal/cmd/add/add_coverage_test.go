// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// PromptPort — pure logic (no Docker) paths
// ---------------------------------------------------------------------------

func TestPromptPort_NoDocker_DefaultPorts(t *testing.T) {
	tests := []struct {
		name     string
		language appdetect.Language
		want     int
	}{
		{
			name:     "Java returns 8080",
			language: appdetect.Java,
			want:     8080,
		},
		{
			name:     "DotNet returns 8080",
			language: appdetect.DotNet,
			want:     8080,
		},
		{
			name:     "Python returns 80",
			language: appdetect.Python,
			want:     80,
		},
		{
			name:     "JavaScript returns 80",
			language: appdetect.JavaScript,
			want:     80,
		},
		{
			name:     "TypeScript returns 80",
			language: appdetect.TypeScript,
			want:     80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			console := mockinput.NewMockConsole()
			prj := appdetect.Project{
				Language: tt.language,
				// Docker is nil → no Dockerfile
			}
			port, err := PromptPort(console, context.Background(), "svc", prj)
			require.NoError(t, err)
			assert.Equal(t, tt.want, port)
		})
	}
}

func TestPromptPort_DockerEmptyPath(t *testing.T) {
	// Docker struct present but Path empty → treated as no Dockerfile
	console := mockinput.NewMockConsole()
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path: "",
		},
	}
	port, err := PromptPort(console, context.Background(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 80, port)
}

func TestPromptPort_DockerSinglePort(t *testing.T) {
	console := mockinput.NewMockConsole()
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path: "/app/Dockerfile",
			Ports: []appdetect.Port{
				{Number: 3000},
			},
		},
	}
	port, err := PromptPort(console, context.Background(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 3000, port)
}

func TestPromptPort_DockerNoPorts_PromptsUser(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("8080")

	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{},
		},
	}
	port, err := PromptPort(console, context.Background(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 8080, port)
}

func TestPromptPort_DockerMultiplePorts_SelectsKnown(t *testing.T) {
	console := mockinput.NewMockConsole()
	// Select the second port (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(1)

	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path: "/app/Dockerfile",
			Ports: []appdetect.Port{
				{Number: 3000},
				{Number: 8080},
			},
		},
	}
	port, err := PromptPort(console, context.Background(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 8080, port)
}

func TestPromptPort_DockerMultiplePorts_SelectsOther(t *testing.T) {
	console := mockinput.NewMockConsole()
	// Select "Other" which is at index len(ports) = 2
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(2)
	// Then prompted for port number
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("9090")

	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path: "/app/Dockerfile",
			Ports: []appdetect.Port{
				{Number: 3000},
				{Number: 8080},
			},
		},
	}
	port, err := PromptPort(console, context.Background(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 9090, port)
}

// ---------------------------------------------------------------------------
// promptPortNumber
// ---------------------------------------------------------------------------

func TestPromptPortNumber_ValidInput(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("443")

	port, err := promptPortNumber(console, context.Background(), "What port?")
	require.NoError(t, err)
	assert.Equal(t, 443, port)
}

func TestPromptPortNumber_BoundaryValues(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want int
	}{
		{"minimum port 1", "1", 1},
		{"maximum port 65535", "65535", 65535},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			console := mockinput.NewMockConsole()
			console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return true
			}).Respond(tt.val)

			port, err := promptPortNumber(console, context.Background(), "port?")
			require.NoError(t, err)
			assert.Equal(t, tt.want, port)
		})
	}
}

// ---------------------------------------------------------------------------
// fillDatabaseName
// ---------------------------------------------------------------------------

func TestFillDatabaseName_AlreadySet(t *testing.T) {
	r := &project.ResourceConfig{
		Name: "existing-db",
		Type: project.ResourceTypeDbPostgres,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillDatabaseName(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "existing-db", result.Name)
}

func TestFillDatabaseName_PromptSuccess(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-db")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbPostgres,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillDatabaseName(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "my-db", result.Name)
}

// ---------------------------------------------------------------------------
// fillOpenAiModelName
// ---------------------------------------------------------------------------

func TestFillOpenAiModelName_AlreadySet(t *testing.T) {
	r := &project.ResourceConfig{
		Name: "gpt-4o",
		Type: project.ResourceTypeOpenAiModel,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillOpenAiModelName(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", result.Name)
}

func TestFillOpenAiModelName_PromptWithDefault(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-model")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeOpenAiModel,
		Props: project.AIModelProps{
			Model: project.AIModelPropsModel{
				Name: "gpt-4o",
			},
		},
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillOpenAiModelName(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "my-model", result.Name)
}

// ---------------------------------------------------------------------------
// fillEventHubs
// ---------------------------------------------------------------------------

func TestFillEventHubs_AlreadyExists(t *testing.T) {
	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingEventHubs,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"event-hubs": {},
			},
		},
	}

	_, err := fillEventHubs(context.Background(), r, nil, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one event hubs")
}

func TestFillEventHubs_ValidName(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-hub")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingEventHubs,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillEventHubs(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "event-hubs", result.Name)
	props, ok := result.Props.(project.EventHubsProps)
	require.True(t, ok)
	assert.Equal(t, []string{"my-hub"}, props.Hubs)
}

// ---------------------------------------------------------------------------
// fillServiceBus
// ---------------------------------------------------------------------------

func TestFillServiceBus_AlreadyExists(t *testing.T) {
	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingServiceBus,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"service-bus": {},
			},
		},
	}

	_, err := fillServiceBus(context.Background(), r, nil, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one service bus")
}

func TestFillServiceBus_ValidName(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-queue")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingServiceBus,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := fillServiceBus(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "service-bus", result.Name)
	props, ok := result.Props.(project.ServiceBusProps)
	require.True(t, ok)
	assert.Equal(t, []string{"my-queue"}, props.Queues)
}

// ---------------------------------------------------------------------------
// fillBlobDetails
// ---------------------------------------------------------------------------

func TestFillBlobDetails_ValidContainerName(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-container")

	modelProps := &project.StorageProps{}
	err := fillBlobDetails(context.Background(), console, modelProps)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-container"}, modelProps.Containers)
}

// ---------------------------------------------------------------------------
// selectFromMap
// ---------------------------------------------------------------------------

func TestSelectFromMap_SingleEntry(t *testing.T) {
	// With only one entry, no console prompt should be needed
	m := map[string]int{"only-one": 42}
	key, val, err := selectFromMap(context.Background(), nil, "pick", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "only-one", key)
	assert.Equal(t, 42, val)
}

func TestSelectFromMap_MultipleEntries(t *testing.T) {
	console := mockinput.NewMockConsole()
	// After sorting, options will be: ["alpha", "beta", "gamma"]
	// Select index 1 → "beta"
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(1)

	m := map[string]string{
		"gamma": "g",
		"alpha": "a",
		"beta":  "b",
	}
	key, val, err := selectFromMap(context.Background(), console, "pick", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "beta", key)
	assert.Equal(t, "b", val)
}

func TestSelectFromMap_WithDefault(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		// Verify the default was set
		assert.Equal(t, "beta", options.DefaultValue)
		return true
	}).Respond(0)

	m := map[string]string{
		"alpha": "a",
		"beta":  "b",
	}
	def := "beta"
	key, _, err := selectFromMap(context.Background(), console, "pick", m, &def)
	require.NoError(t, err)
	assert.Equal(t, "alpha", key)
}

// ---------------------------------------------------------------------------
// selectFromSkus
// ---------------------------------------------------------------------------

func TestSelectFromSkus_Empty(t *testing.T) {
	_, err := selectFromSkus(context.Background(), nil, "pick", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no skus found")
}

func TestSelectFromSkus_Single(t *testing.T) {
	skus := []ModelSku{
		{Name: "Standard", UsageName: "standard"},
	}
	sku, err := selectFromSkus(context.Background(), nil, "pick", skus)
	require.NoError(t, err)
	assert.Equal(t, "Standard", sku.Name)
}

func TestSelectFromSkus_MultipleSelectsFirst(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(0)

	skus := []ModelSku{
		{Name: "Standard", UsageName: "standard"},
		{Name: "GlobalStandard", UsageName: "global-standard"},
	}
	sku, err := selectFromSkus(context.Background(), console, "pick", skus)
	require.NoError(t, err)
	assert.Equal(t, "Standard", sku.Name)
}

func TestSelectFromSkus_MultipleSelectsSecond(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(1)

	skus := []ModelSku{
		{Name: "Standard", UsageName: "standard"},
		{Name: "GlobalStandard", UsageName: "global-standard"},
	}
	sku, err := selectFromSkus(context.Background(), console, "pick", skus)
	require.NoError(t, err)
	assert.Equal(t, "GlobalStandard", sku.Name)
}

// ---------------------------------------------------------------------------
// Configure — additional paths not covered by existing tests
// ---------------------------------------------------------------------------

func TestConfigure_DefaultCase(t *testing.T) {
	// An unknown/unhandled resource type should return the config unchanged
	r := &project.ResourceConfig{
		Name: "custom-resource",
		Type: project.ResourceType("custom.type"),
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "custom-resource", result.Name)
}

func TestConfigure_ExistingResource_Delegates(t *testing.T) {
	// When r.Existing is true, Configure delegates to ConfigureExisting.
	// If Name is already set, ConfigureExisting returns immediately.
	r := &project.ResourceConfig{
		Name:     "my-existing-res",
		Type:     project.ResourceTypeDbPostgres,
		Existing: true,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "my-existing-res", result.Name)
}

func TestConfigure_DbCosmos(t *testing.T) {
	// DbCosmos calls fillDatabaseName then sets CosmosDBProps
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("cosmos-db")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbCosmos,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "cosmos-db", result.Name)
	_, ok := result.Props.(project.CosmosDBProps)
	assert.True(t, ok, "expected CosmosDBProps to be set")
}

func TestConfigure_DbPostgres(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("pg-db")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbPostgres,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "pg-db", result.Name)
}

func TestConfigure_DbMySql(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("mysql-db")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbMySql,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "mysql-db", result.Name)
}

func TestConfigure_DbMongo(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("mongo-db")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbMongo,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "mongo-db", result.Name)
}

func TestConfigure_EventHubs(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("orders-hub")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingEventHubs,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "event-hubs", result.Name)
	props, ok := result.Props.(project.EventHubsProps)
	require.True(t, ok)
	assert.Contains(t, props.Hubs, "orders-hub")
}

func TestConfigure_ServiceBus(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("notifications")

	r := &project.ResourceConfig{
		Type: project.ResourceTypeMessagingServiceBus,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := Configure(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "service-bus", result.Name)
	props, ok := result.Props.(project.ServiceBusProps)
	require.True(t, ok)
	assert.Contains(t, props.Queues, "notifications")
}

// ---------------------------------------------------------------------------
// ConfigureExisting
// ---------------------------------------------------------------------------

func TestConfigureExisting_NameAlreadySet(t *testing.T) {
	r := &project.ResourceConfig{
		Name:     "pre-named",
		Existing: true,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := ConfigureExisting(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "pre-named", result.Name)
}

func TestConfigureExisting_PromptForName(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).Respond("my-resource")

	r := &project.ResourceConfig{
		Existing: true,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
			"providers/Microsoft.Cache/redis/myredis",
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := ConfigureExisting(context.Background(), r, console, p)
	require.NoError(t, err)
	assert.Equal(t, "my-resource", result.Name)
}

// ---------------------------------------------------------------------------
// fillStorageDetails — error paths
// ---------------------------------------------------------------------------

func TestFillStorageDetails_AlreadyExists(t *testing.T) {
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: project.StorageProps{},
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{
				"storage": {},
			},
		},
	}

	_, err := fillStorageDetails(context.Background(), r, nil, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one Storage resource")
}

func TestFillStorageDetails_InvalidProps(t *testing.T) {
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: nil, // not StorageProps
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	_, err := fillStorageDetails(context.Background(), r, nil, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource properties")
}

// ---------------------------------------------------------------------------
// promptUsedBy
// ---------------------------------------------------------------------------

func TestPromptUsedBy_NoHosts(t *testing.T) {
	r := &project.ResourceConfig{
		Name: "my-db",
		Type: project.ResourceTypeDbPostgres,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	uses, err := promptUsedBy(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Nil(t, uses)
}

// ---------------------------------------------------------------------------
// ConfigureLive — non-matching type returns unchanged
// ---------------------------------------------------------------------------

func TestConfigureLive_ExistingResource(t *testing.T) {
	a := &AddAction{}
	r := &project.ResourceConfig{
		Name:     "my-res",
		Existing: true,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := a.ConfigureLive(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "my-res", result.Name)
}

func TestConfigureLive_UnhandledType(t *testing.T) {
	a := &AddAction{}
	r := &project.ResourceConfig{
		Name: "my-db",
		Type: project.ResourceTypeDbPostgres,
	}
	p := PromptOptions{
		PrjConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	result, err := a.ConfigureLive(context.Background(), r, nil, p)
	require.NoError(t, err)
	assert.Equal(t, "my-db", result.Name)
}

// ---------------------------------------------------------------------------
// NewAddCmd
// ---------------------------------------------------------------------------

func TestNewAddCmd_Coverage(t *testing.T) {
	cmd := NewAddCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "add", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
