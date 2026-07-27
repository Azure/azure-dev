// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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

// assertErr returns a canonical error for negative-path tests.
func assertErr() error { return fmt.Errorf("boom") }

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
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeDbPostgres}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillDatabaseName(t.Context(), r, c, opts)
	require.Error(t, err)
}

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
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeOpenAiModel}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillOpenAiModelName(t.Context(), r, c, opts)
	require.Error(t, err)
}

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
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
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
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{Type: project.ResourceTypeMessagingServiceBus}
	opts := PromptOptions{PrjConfig: &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{},
	}}
	_, err := fillServiceBus(t.Context(), r, c, opts)
	require.Error(t, err)
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
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
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
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
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
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	_, err := selectMessaging(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

func TestSelectAiType_SelectError(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	c.WhenSelect(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return 0, assertErr() })
	a := &AddAction{}
	_, err := a.selectAiType(c, t.Context(), PromptOptions{})
	require.Error(t, err)
}

func TestConfigureExisting_PromptsForName(t *testing.T) {
	t.Parallel()
	c := newTestConsole()
	// return whatever default is suggested
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(opts input.ConsoleOptions) (any, error) {
			return opts.DefaultValue, nil
		})
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
			"providers/Microsoft.Cache/Redis/mycache",
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
		Type: project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
			"providers/Microsoft.Cache/Redis/c",
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
	c.WhenPrompt(func(input.ConsoleOptions) bool { return true }).
		RespondFn(func(input.ConsoleOptions) (any, error) { return "", assertErr() })
	r := &project.ResourceConfig{
		Type: project.ResourceTypeDbRedis,
		ResourceId: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
			"providers/Microsoft.Cache/Redis/mycache",
	}
	_, err := ConfigureExisting(t.Context(), r, c, PromptOptions{
		PrjConfig: &project.ProjectConfig{},
	})
	require.Error(t, err)
}

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

// testWriter is a simple bytes.Buffer-like type so we can read resulting
// bytes written by previewWriter without importing bytes into test scope.
type testWriter struct{ b []byte }

func (w *testWriter) String() string { return string(w.b) }

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

func TestConfigureHost_UnsupportedHostType(t *testing.T) {
	t.Parallel()
	a := &AddAction{console: newTestConsole()}
	c := newTestConsole()
	_, _, err := a.configureHost(c, t.Context(), PromptOptions{}, project.ResourceType("not.a.host"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported host type")
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
