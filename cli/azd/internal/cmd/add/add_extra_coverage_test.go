// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func filepath_join(parts ...string) string { return filepath.Join(parts...) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// testConsole embeds *mockinput.MockConsole to provide programmable MultiSelect
// and PromptFs, which MockConsole does not register helpers for.
type testConsole struct {
	*mockinput.MockConsole
	multiSelectFn func(opts input.ConsoleOptions) ([]string, error)
	promptFsFn    func(opts input.ConsoleOptions, fs input.FsOptions) (string, error)
}

func newTestConsole() *testConsole {
	return &testConsole{MockConsole: mockinput.NewMockConsole()}
}

func (c *testConsole) MultiSelect(ctx context.Context, opts input.ConsoleOptions) ([]string, error) {
	if c.multiSelectFn != nil {
		return c.multiSelectFn(opts)
	}
	return c.MockConsole.MultiSelect(ctx, opts)
}

func (c *testConsole) PromptFs(
	ctx context.Context, opts input.ConsoleOptions, fs input.FsOptions,
) (string, error) {
	if c.promptFsFn != nil {
		return c.promptFsFn(opts, fs)
	}
	return c.MockConsole.PromptFs(ctx, opts, fs)
}

// ---------------------------------------------------------------------------
// add.go — selectProvisionOptions
// ---------------------------------------------------------------------------

func TestSelectProvisionOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		selected int
		want     provisionSelection
	}{
		{"preview", 0, provisionPreview},
		{"yes", 1, provision},
		{"no", 2, provisionSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestConsole()
			c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(tt.selected)
			got, err := selectProvisionOptions(t.Context(), c, "prompt?")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSelectProvisionOptions_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	_, err := selectProvisionOptions(t.Context(), c, "prompt?")
	require.Error(t, err)
}

// assertErr returns a canonical error for negative-path tests.
func assertErr() error { return fmt.Errorf("boom") }

// ---------------------------------------------------------------------------
// add_configure_existing.go — resourceType
// ---------------------------------------------------------------------------

func TestResourceType_KnownAndUnknown(t *testing.T) {
	t.Parallel()
	// Known
	got := resourceType("Microsoft.Cache/redis")
	assert.Equal(t, project.ResourceTypeDbRedis, got)

	got = resourceType("Microsoft.KeyVault/vaults")
	assert.Equal(t, project.ResourceTypeKeyVault, got)

	// Unknown
	got = resourceType("Microsoft.DoesNot/exist")
	assert.Equal(t, project.ResourceType(""), got)
}

// ---------------------------------------------------------------------------
// add_configure.go — fillDatabaseName
// ---------------------------------------------------------------------------

func TestFillDatabaseName_PromptsAndAccepts(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("app-db")

	r := &project.ResourceConfig{Type: project.ResourceTypeDbPostgres}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillDatabaseName(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "app-db", got.Name)
}

func TestFillDatabaseName_RetriesOnInvalidName(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"Bad Name!", "good-db"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	r := &project.ResourceConfig{Type: project.ResourceTypeDbMongo}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillDatabaseName(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "good-db", got.Name)
	assert.Equal(t, 2, i, "should have prompted twice")
}

func TestFillDatabaseName_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeDbPostgres}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillDatabaseName(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure.go — fillOpenAiModelName
// ---------------------------------------------------------------------------

func TestFillOpenAiModelName_DefaultAccepted(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			return opts.DefaultValue, nil
		})
	r := &project.ResourceConfig{
		Type: project.ResourceTypeOpenAiModel,
		Props: project.AIModelProps{
			Model: project.AIModelPropsModel{Name: "gpt-4o", Version: "2024"},
		},
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillOpenAiModelName(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", got.Name)
}

func TestFillOpenAiModelName_DefaultCollision(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	// Capture the default value shown to the user to assert the collision logic
	// appends a "-2" suffix.
	var shownDefault string
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			if s, ok := opts.DefaultValue.(string); ok {
				shownDefault = s
			}
			return opts.DefaultValue, nil
		})
	r := &project.ResourceConfig{
		Type: project.ResourceTypeOpenAiModel,
		Props: project.AIModelProps{
			Model: project.AIModelPropsModel{Name: "gpt-4o", Version: "2024"},
		},
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"gpt-4o": {Name: "gpt-4o"},
		},
	}}
	_, err := fillOpenAiModelName(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o-2", shownDefault)
}

func TestFillOpenAiModelName_InvalidNameRetries(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"Bad Name", "my-model"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	r := &project.ResourceConfig{Type: project.ResourceTypeOpenAiModel}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillOpenAiModelName(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "my-model", got.Name)
	assert.Equal(t, 2, i)
}

func TestFillOpenAiModelName_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeOpenAiModel}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillOpenAiModelName(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure_messaging.go — fillEventHubs / fillServiceBus full flow
// ---------------------------------------------------------------------------

func TestFillEventHubs_PromptsAndSetsProps(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("orders")
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingEventHubs}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillEventHubs(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "event-hubs", got.Name)
	props, ok := got.Props.(project.EventHubsProps)
	require.True(t, ok)
	assert.Equal(t, []string{"orders"}, props.Hubs)
}

func TestFillEventHubs_ValidationRetry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"Bad Name!", "orders"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingEventHubs}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillEventHubs(t.Context(), r, c, opts)
	require.NoError(t, err)
	props, ok := got.Props.(project.EventHubsProps)
	require.True(t, ok)
	assert.Equal(t, []string{"orders"}, props.Hubs)
}

func TestFillEventHubs_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingEventHubs}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillEventHubs(t.Context(), r, c, opts)
	require.Error(t, err)
}

func TestFillServiceBus_PromptsAndSetsProps(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("orders-queue")
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingServiceBus}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillServiceBus(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "service-bus", got.Name)
	props, ok := got.Props.(project.ServiceBusProps)
	require.True(t, ok)
	assert.Equal(t, []string{"orders-queue"}, props.Queues)
}

func TestFillServiceBus_ValidationRetry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"Bad Name!", "my-queue"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingServiceBus}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillServiceBus(t.Context(), r, c, opts)
	require.NoError(t, err)
	props, ok := got.Props.(project.ServiceBusProps)
	require.True(t, ok)
	assert.Equal(t, []string{"my-queue"}, props.Queues)
}

func TestFillServiceBus_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingServiceBus}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillServiceBus(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure_storage.go — selectStorageDataTypes / fillBlobDetails /
// fillStorageDetails
// ---------------------------------------------------------------------------

func TestSelectStorageDataTypes_ReturnsBlobs(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return []string{StorageDataTypeBlob}, nil
	}
	got, err := selectStorageDataTypes(t.Context(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{StorageDataTypeBlob}, got)
}

func TestSelectStorageDataTypes_RetriesOnEmpty(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	calls := 0
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		calls++
		if calls == 1 {
			return []string{}, nil
		}
		return []string{StorageDataTypeBlob}, nil
	}
	got, err := selectStorageDataTypes(t.Context(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{StorageDataTypeBlob}, got)
	assert.Equal(t, 2, calls)
}

func TestSelectStorageDataTypes_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return nil, assertErr()
	}
	_, err := selectStorageDataTypes(t.Context(), c)
	require.Error(t, err)
}

func TestFillBlobDetails_SuccessAndRetry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"bad container!", "my-blobs"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	props := &project.StorageProps{}
	err := fillBlobDetails(t.Context(), c, props)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-blobs"}, props.Containers)
}

func TestFillBlobDetails_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	props := &project.StorageProps{}
	err := fillBlobDetails(t.Context(), c, props)
	require.Error(t, err)
}

func TestFillStorageDetails_FullFlow(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return []string{StorageDataTypeBlob}, nil
	}
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("my-container")
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: project.StorageProps{},
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := fillStorageDetails(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, "storage", got.Name)
	props, ok := got.Props.(project.StorageProps)
	require.True(t, ok)
	assert.Equal(t, []string{"my-container"}, props.Containers)
}

func TestFillStorageDetails_MultiSelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return nil, assertErr()
	}
	r := &project.ResourceConfig{
		Type:  project.ResourceTypeStorage,
		Props: project.StorageProps{},
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillStorageDetails(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_select.go — selectDatabase / selectHost / selectMessaging
// ---------------------------------------------------------------------------

func TestSelectDatabase_FirstOption(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(0)
	r, err := selectDatabase(c, t.Context(), PromptOptions{})
	require.NoError(t, err)
	// The list is sorted by display name; the specific type is not important
	// but it should be a db.* type.
	assert.True(t, len(string(r.Type)) > 0)
	assert.Contains(t, string(r.Type), "db.")
}

func TestSelectDatabase_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	_, err := selectDatabase(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

func TestSelectHost_DefaultIsContainerApp(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(0)
	r, err := selectHost(c, t.Context(), PromptOptions{})
	require.NoError(t, err)
	// index 0 is the default (ContainerApp).
	assert.Equal(t, project.ResourceTypeHostContainerApp, r.Type)
}

func TestSelectHost_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	_, err := selectHost(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

func TestSelectMessaging_FirstOption(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(0)
	r, err := selectMessaging(c, t.Context(), PromptOptions{})
	require.NoError(t, err)
	assert.Contains(t, string(r.Type), "messaging.")
}

func TestSelectMessaging_Error(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	_, err := selectMessaging(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_select.go — selectAiType
// ---------------------------------------------------------------------------

func TestSelectAiType_Branches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		selected int
		wantType project.ResourceType
	}{
		{"openai", 0, project.ResourceTypeOpenAiModel},
		{"other", 1, project.ResourceTypeAiProject},
		{"search", 2, project.ResourceTypeAiSearch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestConsole()
			c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(tt.selected)
			a := &AddAction{}
			r, err := a.selectAiType(c, t.Context(), PromptOptions{})
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, r.Type)
		})
	}
}

func TestSelectAiType_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	a := &AddAction{}
	_, err := a.selectAiType(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure_existing.go — ConfigureExisting empty name path
// ---------------------------------------------------------------------------

func TestConfigureExisting_PromptsForName(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	// return whatever default is suggested
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			return opts.DefaultValue, nil
		})
	r := &project.ResourceConfig{
		Type:       project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Cache/Redis/mycache",
	}
	got, err := ConfigureExisting(t.Context(), r, c, PromptOptions{
		PrjConfig: &project.ProjectConfig{},
	})
	require.NoError(t, err)
	assert.Equal(t, "mycache", got.Name)
}

func TestConfigureExisting_RetriesOnInvalidName(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"Invalid Name!", "good-name"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	r := &project.ResourceConfig{
		Type:       project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Cache/Redis/c",
	}
	got, err := ConfigureExisting(t.Context(), r, c, PromptOptions{
		PrjConfig: &project.ProjectConfig{},
	})
	require.NoError(t, err)
	assert.Equal(t, "good-name", got.Name)
	assert.Equal(t, 2, i)
}

func TestConfigureExisting_InvalidResourceId(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:       project.ResourceTypeDbRedis,
		ResourceId: "not-a-valid-id",
	}
	_, err := ConfigureExisting(t.Context(), r, newTestConsole(), PromptOptions{
		PrjConfig: &project.ProjectConfig{},
	})
	require.Error(t, err)
}

func TestConfigureExisting_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{
		Type:       project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Cache/Redis/mycache",
	}
	_, err := ConfigureExisting(t.Context(), r, c, PromptOptions{
		PrjConfig: &project.ProjectConfig{},
	})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure_host.go — PromptPort (Docker ports branches)
// ---------------------------------------------------------------------------

func TestPromptPort_MultiplePorts_SelectSpecific(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(1)
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 8080, port)
}

func TestPromptPort_MultiplePorts_OtherPrompts(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	// Select 'Other' (last option, index 2 for two ports + Other).
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(2)
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("4000")
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 4000, port)
}

func TestPromptPort_NoPortsExposed_PromptsNumber(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("5000")
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker:   &appdetect.Docker{Path: "/app/Dockerfile"},
	}
	port, err := PromptPort(c, t.Context(), "svc", prj)
	require.NoError(t, err)
	assert.Equal(t, 5000, port)
}

func TestPromptPort_MultiplePorts_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	_, err := PromptPort(c, t.Context(), "svc", prj)
	require.Error(t, err)
}

func TestPromptPort_MultiplePorts_OtherPromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(2)
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	prj := appdetect.Project{
		Language: appdetect.Python,
		Docker: &appdetect.Docker{
			Path:  "/app/Dockerfile",
			Ports: []appdetect.Port{{Number: 3000}, {Number: 8080}},
		},
	}
	_, err := PromptPort(c, t.Context(), "svc", prj)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure_host.go — promptPortNumber validation and error paths
// ---------------------------------------------------------------------------

func TestPromptPortNumber_ValidFirstTry(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("8080")
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 8080, p)
}

func TestPromptPortNumber_NonIntegerThenValid(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"abc", "1234"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 1234, p)
}

func TestPromptPortNumber_OutOfRangeThenValid(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	responses := []string{"0", "70000", "443"}
	i := 0
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			v := responses[i]
			i++
			return v, nil
		})
	p, err := promptPortNumber(c, t.Context(), "port?")
	require.NoError(t, err)
	assert.Equal(t, 443, p)
}

func TestPromptPortNumber_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	_, err := promptPortNumber(c, t.Context(), "port?")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure.go — fillUses via Configure(host type)
// ---------------------------------------------------------------------------

func TestFillUses_LinkToOtherResources(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		// Select the two non-host resources; values must match the formatted
		// labels produced by fillUses ("[<Type>]\t<name>").
		return opts.Options, nil
	}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp,
		Name: "web",
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"redis":    {Type: project.ResourceTypeDbRedis, Name: "redis"},
			"postgres": {Type: project.ResourceTypeDbPostgres, Name: "postgres"},
			// Different host type should be filtered out.
			"legacy": {Type: project.ResourceTypeHostAppService, Name: "legacy"},
		},
	}}
	got, err := fillUses(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"redis", "postgres"}, got.Uses)
}

func TestFillUses_MultiSelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return nil, assertErr()
	}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp,
		Name: "web",
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"redis": {Type: project.ResourceTypeDbRedis, Name: "redis"},
		},
	}}
	_, err := fillUses(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure.go — promptUsedBy select flow
// ---------------------------------------------------------------------------

func TestPromptUsedBy_ReturnsSelectedServices(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return []string{"web"}, nil
	}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"web": {Type: project.ResourceTypeHostContainerApp, Name: "web"},
		},
	}}
	got, err := promptUsedBy(t.Context(), r, c, opts)
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, got)
}

func TestPromptUsedBy_MultiSelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		return nil, assertErr()
	}
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		Name: "redis",
	}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"web": {Type: project.ResourceTypeHostContainerApp, Name: "web"},
		},
	}}
	_, err := promptUsedBy(t.Context(), r, c, opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_preview.go — Metadata coverage (host + existing)
// ---------------------------------------------------------------------------

func TestMetadata_HostPrefix(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp,
		Name: "api",
	}
	md := Metadata(r)
	assert.Equal(t, "Microsoft.App/containerApps", md.ResourceType)
	// Host resources use uppercase name as their prefix.
	for _, v := range md.Variables {
		assert.Contains(t, v, "API_")
	}
}

func TestMetadata_ExistingAppendsName(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:     project.ResourceTypeDbRedis,
		Name:     "mycache",
		Existing: true,
	}
	md := Metadata(r)
	assert.Equal(t, "Microsoft.Cache/redis", md.ResourceType)
	// The existing suffix encodes the resource name.
	for _, v := range md.Variables {
		assert.Contains(t, v, "MYCACHE")
	}
}

func TestMetadata_UnknownResourceReturnsEmpty(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceType("unknown.type"),
		Name: "thing",
	}
	md := Metadata(r)
	assert.Equal(t, metaDisplay{}, md)
}

// ---------------------------------------------------------------------------
// add_preview.go — previewWriter additional control-character transformations
// ---------------------------------------------------------------------------

func TestPreviewWriter_BoldAndGreenControlChars(t *testing.T) {
	t.Parallel()
	// Using "b" (bold) and "g" (green) control chars — these are stripped
	// from the output and replaced with a space.
	tests := []struct {
		name string
		in   string
	}{
		{"bold", "b  bold line\n"},
		{"green", "g  green line\n"},
		{"minus", "-  removed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf testWriter
			pw := &previewWriter{w: &buf}
			_, err := pw.Write([]byte(tt.in))
			require.NoError(t, err)
			// Output should contain some of the visible text.
			out := buf.String()
			assert.NotEmpty(t, out, "input: %q", tt.in)
		})
	}
}

// testWriter is a simple bytes.Buffer-like type so we can read resulting
// bytes written by previewWriter without importing bytes into test scope.
type testWriter struct{ b []byte }

func (w *testWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
func (w *testWriter) String() string { return string(w.b) }

// ---------------------------------------------------------------------------
// add_select_ai.go — selectFromMap with multiple options (Select path)
// ---------------------------------------------------------------------------

func TestSelectFromMap_MultipleOptions(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// options are sorted, return index 0
			return 0, nil
		})
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	key, val, err := selectFromMap(t.Context(), c, "q", m, nil)
	require.NoError(t, err)
	assert.Equal(t, "a", key)
	assert.Equal(t, 1, val)
}

func TestSelectFromMap_MultipleOptions_WithDefault(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			assert.Equal(t, "b", opts.DefaultValue)
			return 1, nil
		})
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	def := "b"
	key, _, err := selectFromMap(t.Context(), c, "q", m, &def)
	require.NoError(t, err)
	assert.Equal(t, "b", key)
}

func TestSelectFromMap_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	m := map[string]int{"a": 1, "b": 2}
	_, _, err := selectFromMap(t.Context(), c, "q", m, nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_select_ai.go — selectFromSkus with multiple options
// ---------------------------------------------------------------------------

func TestSelectFromSkus_Multiple(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).Respond(1)
	skus := []ModelSku{
		{Name: "Standard"},
		{Name: "Premium"},
	}
	got, err := selectFromSkus(t.Context(), c, "q", skus)
	require.NoError(t, err)
	assert.Equal(t, "Premium", got.Name)
}

func TestSelectFromSkus_MultipleError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	skus := []ModelSku{{Name: "Standard"}, {Name: "Premium"}}
	_, err := selectFromSkus(t.Context(), c, "q", skus)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure.go — Configure: AiSearch duplicate / auto name
// ---------------------------------------------------------------------------

func TestConfigure_AiSearchDuplicate(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{Type: project.ResourceTypeAiSearch}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{"search": {}},
	}}
	_, err := Configure(t.Context(), r, nil, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one AI Search")
}

func TestConfigure_AiSearchSetsName(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{Type: project.ResourceTypeAiSearch}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	got, err := Configure(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "search", got.Name)
}

// ---------------------------------------------------------------------------
// add_configure.go — fillAiProjectName with collision
// ---------------------------------------------------------------------------

func TestFillAiProjectName_Collision(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{Type: project.ResourceTypeAiProject}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"ai-project": {Name: "ai-project"},
		},
	}}
	got, err := fillAiProjectName(t.Context(), r, nil, opts)
	require.NoError(t, err)
	assert.Equal(t, "ai-project-2", got.Name)
}

// ---------------------------------------------------------------------------
// add_configure_host.go — addServiceAsResource AppService happy paths
// ---------------------------------------------------------------------------

func TestAddServiceAsResource_AppService_Python(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "py-svc",
		Host:         project.AppServiceTarget,
		Language:     project.ServiceLanguagePython,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.Python}
	r, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.NoError(t, err)
	assert.Equal(t, project.ResourceTypeHostAppService, r.Type)
	props, ok := r.Props.(project.AppServiceProps)
	require.True(t, ok)
	assert.Equal(t, 80, props.Port)
	assert.Equal(t, project.AppServiceRuntimeStackPython, props.Runtime.Stack)
}

func TestAddServiceAsResource_AppService_JavaScript(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "js-svc",
		Host:         project.AppServiceTarget,
		Language:     project.ServiceLanguageJavaScript,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.JavaScript}
	r, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.NoError(t, err)
	props, ok := r.Props.(project.AppServiceProps)
	require.True(t, ok)
	assert.Equal(t, project.AppServiceRuntimeStackNode, props.Runtime.Stack)
	assert.Equal(t, 80, props.Port)
}

// ---------------------------------------------------------------------------
// util.go — promptDir / promptDockerfile
// ---------------------------------------------------------------------------

func TestPromptDir_ReturnsAbsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(opts input.ConsoleOptions, _ input.FsOptions) (string, error) {
		return dir, nil
	}
	got, err := promptDir(t.Context(), c, "pick dir")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDir_RetriesOnInvalidThenAccepts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	calls := 0
	c := newTestConsole()
	c.promptFsFn = func(opts input.ConsoleOptions, _ input.FsOptions) (string, error) {
		calls++
		if calls == 1 {
			return filepath_join(dir, "nope-does-not-exist"), nil
		}
		return dir, nil
	}
	got, err := promptDir(t.Context(), c, "pick dir")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, 2, calls)
}

func TestPromptDir_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	_, err := promptDir(t.Context(), c, "pick dir")
	require.Error(t, err)
}

func TestPromptDockerfile_DirectFilePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dockerPath := filepath_join(dir, "Dockerfile")
	writeFile(t, dockerPath, "FROM scratch\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return dockerPath, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDockerfile_DirWithDockerfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath_join(dir, "Dockerfile"), "FROM scratch\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return dir, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestPromptDockerfile_RetriesWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath_join(dir, "Dockerfile"), "FROM scratch\n")
	calls := 0
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		calls++
		if calls == 1 {
			return filepath_join(dir, "nothing-here"), nil
		}
		return dir, nil
	}
	got, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Equal(t, 2, calls)
}

func TestPromptDockerfile_PromptError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	_, err := promptDockerfile(t.Context(), c, "pick dockerfile")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// add_configure.go — fillUses interactive formatting path
// ---------------------------------------------------------------------------

func TestFillUses_InteractiveTabAlign(t *testing.T) {
	t.Parallel()
	prj := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"redis":    {Name: "redis", Type: project.ResourceTypeDbRedis},
			"postgres": {Name: "postgres", Type: project.ResourceTypeDbPostgres},
		},
	}
	r := &project.ResourceConfig{Name: "api", Type: project.ResourceTypeHostContainerApp}
	c := newTestConsole()
	c.MockConsole.SetTerminal(true) // exercise TabAlign interactive path
	c.multiSelectFn = func(opts input.ConsoleOptions) ([]string, error) {
		if len(opts.Options) == 0 {
			return nil, nil
		}
		return []string{opts.Options[0]}, nil
	}
	got, err := fillUses(t.Context(), r, c, PromptOptions{PrjConfig: prj})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.NotEmpty(t, got.Uses)
}

// ---------------------------------------------------------------------------
// add_configure_host.go — addServiceAsResource unsupported host
// ---------------------------------------------------------------------------

func TestAddServiceAsResource_UnsupportedHost(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	svc := &project.ServiceConfig{
		Name:         "svc",
		Host:         project.ServiceTargetKind("bogus"),
		Language:     project.ServiceLanguageJavaScript,
		RelativePath: tempDir,
	}
	prj := appdetect.Project{Language: appdetect.JavaScript}
	_, err := addServiceAsResource(t.Context(), c, svc, prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service target")
}

// ---------------------------------------------------------------------------
// diff.go — formatLine equal/insert/delete
// ---------------------------------------------------------------------------

func TestFormatLine_AllOps(t *testing.T) {
	t.Parallel()
	oldM := map[string]*project.ResourceConfig{
		"keep":    {Name: "keep", Type: project.ResourceTypeDbRedis},
		"changed": {Name: "changed", Type: project.ResourceTypeDbPostgres},
	}
	newM := map[string]*project.ResourceConfig{
		"keep":    {Name: "keep", Type: project.ResourceTypeDbRedis},
		"changed": {Name: "changed", Type: project.ResourceTypeDbMongo},
		"added":   {Name: "added", Type: project.ResourceTypeKeyVault},
	}
	s, err := DiffBlocks(oldM, newM)
	require.NoError(t, err)
	assert.NotEmpty(t, s)
}

// ---------------------------------------------------------------------------
// add.go — NewAddAction constructs the action (smoke test)
// ---------------------------------------------------------------------------

func TestNewAddAction_Constructs(t *testing.T) {
	t.Parallel()
	// Pass nils for all deps — this is a no-op constructor that only
	// assigns fields; no methods are invoked.
	a := NewAddAction(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, a)
}

// ---------------------------------------------------------------------------
// add_configure.go — ConfigureLive paths that don't hit live deps
// ---------------------------------------------------------------------------

func TestConfigureLive_ExistingReturnsAsIs(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r := &project.ResourceConfig{Existing: true, Name: "x", Type: project.ResourceTypeDbRedis}
	c := newTestConsole()
	got, err := a.ConfigureLive(t.Context(), r, c, PromptOptions{})
	require.NoError(t, err)
	assert.Same(t, r, got)
}

func TestConfigureLive_NonLiveTypePassthrough(t *testing.T) {
	t.Parallel()
	a := &AddAction{}
	r := &project.ResourceConfig{Name: "x", Type: project.ResourceTypeDbRedis}
	c := newTestConsole()
	got, err := a.ConfigureLive(t.Context(), r, c, PromptOptions{})
	require.NoError(t, err)
	assert.Same(t, r, got)
}

// ---------------------------------------------------------------------------
// add_configure.go — Configure with unknown type returns r unchanged
// ---------------------------------------------------------------------------

func TestConfigure_UnknownTypePassthrough(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{Name: "x", Type: project.ResourceType("custom.thing")}
	c := newTestConsole()
	got, err := Configure(t.Context(), r, c, PromptOptions{PrjConfig: &project.ProjectConfig{}})
	require.NoError(t, err)
	assert.Same(t, r, got)
}

func TestConfigure_KeyVaultDuplicateReturnsError(t *testing.T) {
	t.Parallel()
	prj := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {Name: "vault", Type: project.ResourceTypeKeyVault},
		},
	}
	r := &project.ResourceConfig{Name: "new", Type: project.ResourceTypeKeyVault}
	c := newTestConsole()
	_, err := Configure(t.Context(), r, c, PromptOptions{PrjConfig: prj})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault")
}

func TestConfigure_RedisDuplicateReturnsError(t *testing.T) {
	t.Parallel()
	prj := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"redis": {Name: "redis", Type: project.ResourceTypeDbRedis},
		},
	}
	r := &project.ResourceConfig{Name: "new", Type: project.ResourceTypeDbRedis}
	c := newTestConsole()
	_, err := Configure(t.Context(), r, c, PromptOptions{PrjConfig: prj})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Redis")
}

// ---------------------------------------------------------------------------
// add.go — ensureCompatibleProject additional paths
// ---------------------------------------------------------------------------

func TestEnsureCompatibleProject_NoInfraNoResources(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	prj := &project.ProjectConfig{
		Path: tempDir,
	}
	// importManager without an AppHost (empty config) returns false.
	im := project.NewImportManager(nil)
	err := ensureCompatibleProject(t.Context(), im, prj)
	require.NoError(t, err)
}

func TestEnsureCompatibleProject_InfraWithoutResources(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	infraDir := filepath_join(tempDir, "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0o755))
	writeFile(t, filepath_join(infraDir, "main.bicep"), "// bicep\n")
	prj := &project.ProjectConfig{
		Path: tempDir,
	}
	im := project.NewImportManager(nil)
	err := ensureCompatibleProject(t.Context(), im, prj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible project")
}

func TestEnsureCompatibleProject_InfraWithResources(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	infraDir := filepath_join(tempDir, "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0o755))
	writeFile(t, filepath_join(infraDir, "main.bicep"), "// bicep\n")
	prj := &project.ProjectConfig{
		Path: tempDir,
		Resources: map[string]*project.ResourceConfig{
			"redis": {Name: "redis", Type: project.ResourceTypeDbRedis},
		},
	}
	im := project.NewImportManager(nil)
	err := ensureCompatibleProject(t.Context(), im, prj)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// add_preview.go — previewWriter plus/space line control chars
// ---------------------------------------------------------------------------

func TestPreviewWriter_PlusAndSpace(t *testing.T) {
	t.Parallel()
	var buf testWriter
	pw := &previewWriter{w: &buf}
	_, err := pw.Write([]byte("+  added line\n"))
	require.NoError(t, err)
	_, err = pw.Write([]byte("   unchanged line\n"))
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

// ---------------------------------------------------------------------------
// add_configure.go — fillUses with only cross-host resources (filtered out)
// ---------------------------------------------------------------------------

func TestFillUses_CrossHostSkipped(t *testing.T) {
	t.Parallel()
	// r is AppService; a ContainerApp in the project should be skipped.
	prj := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"other-host": {Name: "other-host", Type: project.ResourceTypeHostContainerApp},
		},
	}
	r := &project.ResourceConfig{Name: "web", Type: project.ResourceTypeHostAppService}
	c := newTestConsole()
	// The other-host is cross-host + not same type, so filter removes it;
	// but r itself isn't in p.PrjConfig.Resources so nothing is left.
	// Since isHost==true and the only other is cross-host, no options: no MultiSelect.
	got, err := fillUses(t.Context(), r, c, PromptOptions{PrjConfig: prj})
	require.NoError(t, err)
	assert.Empty(t, got.Uses)
}

// ---------------------------------------------------------------------------
// add_configure_host.go — configureHost unsupported host type
// ---------------------------------------------------------------------------

func TestConfigureHost_UnsupportedHostType(t *testing.T) {
	t.Parallel()
	a := &AddAction{console: newTestConsole()}
	c := newTestConsole()
	_, _, err := a.configureHost(c, t.Context(), PromptOptions{}, project.ResourceType("not.a.host"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported host type")
}

// ---------------------------------------------------------------------------
// add_configure_host.go — promptCodeProject fallback language selection
// ---------------------------------------------------------------------------

func TestPromptCodeProject_FallbackLanguageSelection(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	// Write a requirements.txt so Python selection succeeds.
	writeFile(t, filepath_join(tempDir, "requirements.txt"), "flask\n")
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	// Respond Select with index 0 — whatever language first lands alphabetically.
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// Pick the first Python-tagged option to get requirements.txt path exercised;
			// fall back to 0 if not found.
			for i, o := range opts.Options {
				if containsCI(o, "Python") {
					return i, nil
				}
			}
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	require.NoError(t, err)
	require.NotNil(t, prj)
	assert.Equal(t, tempDir, prj.Path)
}

func TestPromptCodeProject_PromptDirError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return "", assertErr()
	}
	a := &AddAction{console: c}
	_, err := a.promptCodeProject(t.Context())
	require.Error(t, err)
}

func containsCI(hay, needle string) bool {
	return stringsIndexFold(hay, needle) >= 0
}

// stringsIndexFold does a case-insensitive substring search.
func stringsIndexFold(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	// Naive search; adequate for tests.
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a := s[i+j]
			b := substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestPromptCodeProject_ManualFallback_Java(t *testing.T) {
	t.Parallel()
	// Empty dir so appdetect returns nil.
	tempDir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			// Pick a non-Python language to avoid requirements.txt check.
			for i, o := range opts.Options {
				if containsCI(o, "Java") && !containsCI(o, "JavaScript") {
					return i, nil
				}
			}
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	require.NoError(t, err)
	require.NotNil(t, prj)
	assert.Equal(t, "Manual", prj.DetectionRule)
}

func TestPromptCodeProject_ManualFallback_InteractiveTabAlign(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	c.MockConsole.SetTerminal(true)
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			return 0, nil
		})
	a := &AddAction{console: c}
	prj, err := a.promptCodeProject(t.Context())
	// Either Python without requirements.txt (error) or non-Python success.
	// Both exercise the TabAlign path.
	if err == nil {
		require.NotNil(t, prj)
	} else {
		assert.Contains(t, err.Error(), "requirements.txt")
	}
}

func TestPromptCodeProject_ManualFallback_SelectError(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	c := newTestConsole()
	c.promptFsFn = func(input.ConsoleOptions, input.FsOptions) (string, error) {
		return tempDir, nil
	}
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	a := &AddAction{console: c}
	_, err := a.promptCodeProject(t.Context())
	require.Error(t, err)
}
