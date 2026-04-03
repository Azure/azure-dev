// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// extensionShowItem.Display tests
// ---------------------------------------------------------------------------

func Test_ExtensionShowItem_Display_Minimal(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:          "test.ext",
		Name:        "Test Extension",
		Description: "A test extension",
		Source:      "azd",
		Namespace:   "test",
		Usage:       "azd test",
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test.ext")
	assert.Contains(t, buf.String(), "Test Extension")
	assert.Contains(t, buf.String(), "azd test")
}

func Test_ExtensionShowItem_Display_AllFields(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:                "full.ext",
		Name:              "Full Extension",
		Description:       "Full desc",
		Source:            "custom-src",
		Namespace:         "full",
		Website:           "https://example.com",
		LatestVersion:     "2.0.0",
		InstalledVersion:  "1.0.0",
		AvailableVersions: []string{"1.0.0", "1.5.0", "2.0.0"},
		Tags:              []string{"tool", "testing"},
		Usage:             "azd full do-thing",
		Capabilities:      []extensions.CapabilityType{"mcp"},
		Providers: []extensions.Provider{
			{Name: "prov1", Type: "host", Description: "Provider 1"},
		},
		Examples: []extensions.ExtensionExample{
			{Usage: "azd full example1"},
			{Usage: "azd full example2"},
		},
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "https://example.com")
	assert.Contains(t, out, "2.0.0")
	assert.Contains(t, out, "1.0.0")
	assert.Contains(t, out, "tool")
	assert.Contains(t, out, "testing")
	assert.Contains(t, out, "mcp")
	assert.Contains(t, out, "prov1")
	assert.Contains(t, out, "Provider 1")
	assert.Contains(t, out, "azd full example1")
	assert.Contains(t, out, "azd full example2")
}

func Test_ExtensionShowItem_Display_NoWebsite(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:          "test.ext",
		Name:        "Test",
		Description: "Desc",
		Source:      "s",
		Namespace:   "n",
		Usage:       "azd test",
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	// Website row should not appear
	assert.NotContains(t, buf.String(), "Website")
}

func Test_ExtensionShowItem_Display_EmptyCapabilities(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id: "x", Name: "X", Description: "D", Source: "s", Namespace: "n",
		Usage:        "u",
		Capabilities: []extensions.CapabilityType{},
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "Capabilities")
}

// ---------------------------------------------------------------------------
// promptForExtensionChoice tests
// ---------------------------------------------------------------------------

func Test_PromptForExtensionChoice_Empty(t *testing.T) {
	t.Parallel()
	_, err := promptForExtensionChoice(context.Background(), mockinput.NewMockConsole(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no extensions")
}

func Test_PromptForExtensionChoice_Single(t *testing.T) {
	t.Parallel()
	ext := &extensions.ExtensionMetadata{Id: "my.ext", DisplayName: "My Ext"}
	result, err := promptForExtensionChoice(
		context.Background(), mockinput.NewMockConsole(),
		[]*extensions.ExtensionMetadata{ext},
	)
	require.NoError(t, err)
	assert.Equal(t, "my.ext", result.Id)
}

func Test_PromptForExtensionChoice_Multiple_SelectFirst(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "ext.a", DisplayName: "Ext A", Source: "s", Description: "A"},
		{Id: "ext.b", DisplayName: "Ext B", Source: "s", Description: "B"},
	}
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)
	result, err := promptForExtensionChoice(context.Background(), console, exts)
	require.NoError(t, err)
	assert.Equal(t, "ext.a", result.Id)
}

func Test_PromptForExtensionChoice_Multiple_SelectSecond(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "ext.a", DisplayName: "Ext A", Source: "s", Description: "A"},
		{Id: "ext.b", DisplayName: "Ext B", Source: "s", Description: "B"},
	}
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)
	result, err := promptForExtensionChoice(context.Background(), console, exts)
	require.NoError(t, err)
	assert.Equal(t, "ext.b", result.Id)
}

func Test_PromptForExtensionChoice_Multiple_Error(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "ext.a", DisplayName: "Ext A", Source: "s", Description: "A"},
		{Id: "ext.b", DisplayName: "Ext B", Source: "s", Description: "B"},
	}
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(_ input.ConsoleOptions) (any, error) { return 0, fmt.Errorf("cancelled") })
	_, err := promptForExtensionChoice(context.Background(), console, exts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// prepareHook tests
// ---------------------------------------------------------------------------

func Test_PrepareHook_NoPlatform(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{}}
	hook := &ext.HookConfig{Run: "echo hello"}
	err := action.prepareHook("test-hook", hook)
	require.NoError(t, err)
	assert.Equal(t, "test-hook", hook.Name)
}

func Test_PrepareHook_Windows(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{platform: "windows"}}
	hook := &ext.HookConfig{
		Windows: &ext.HookConfig{Run: "echo win"},
	}
	err := action.prepareHook("h1", hook)
	require.NoError(t, err)
	assert.Equal(t, "echo win", hook.Run)
}

func Test_PrepareHook_Windows_NotConfigured(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{platform: "windows"}}
	hook := &ext.HookConfig{Run: "echo default"}
	err := action.prepareHook("h1", hook)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured for Windows")
}

func Test_PrepareHook_Posix(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{platform: "posix"}}
	hook := &ext.HookConfig{
		Posix: &ext.HookConfig{Run: "echo posix"},
	}
	err := action.prepareHook("h2", hook)
	require.NoError(t, err)
	assert.Equal(t, "echo posix", hook.Run)
}

func Test_PrepareHook_Posix_NotConfigured(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{platform: "posix"}}
	hook := &ext.HookConfig{Run: "echo default"}
	err := action.prepareHook("h2", hook)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured for Posix")
}

func Test_PrepareHook_InvalidPlatform(t *testing.T) {
	t.Parallel()
	action := &hooksRunAction{flags: &hooksRunFlags{platform: "badplatform"}}
	hook := &ext.HookConfig{Run: "echo"}
	err := action.prepareHook("h3", hook)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "badplatform")
}

// ---------------------------------------------------------------------------
// envSetSecretAction.Run - missing args early return
// ---------------------------------------------------------------------------

func Test_EnvSetSecretAction_NoArgs(t *testing.T) {
	t.Parallel()
	action := &envSetSecretAction{args: []string{}}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrNoArgsProvided)
}

func Test_EnvSetSecretAction_WithArgs_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(_ input.ConsoleOptions) (any, error) { return 0, fmt.Errorf("cancelled") })
	action := &envSetSecretAction{
		args:    []string{"MY_SECRET"},
		console: console,
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting secret setting strategy")
}

// ---------------------------------------------------------------------------
// extensionSourceRemoveAction.Run - early arg validation
// ---------------------------------------------------------------------------

func Test_ExtensionSourceRemoveAction_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceRemoveAction{args: []string{}}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceRemoveAction_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceRemoveAction{args: []string{"a", "b"}}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

// ---------------------------------------------------------------------------
// extensionSourceValidateAction.Run - early arg validation
// ---------------------------------------------------------------------------

func Test_ExtensionSourceValidateAction_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{args: []string{}}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceValidateAction_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{args: []string{"a", "b"}}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

// ---------------------------------------------------------------------------
// extensionAction.Run (extensions.go) - missing annotation
// ---------------------------------------------------------------------------

func Test_ExtensionAction_MissingAnnotation(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	action := &extensionAction{cmd: cmd}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrExtensionNotFound)
}

// ---------------------------------------------------------------------------
// extensionSourceListAction.Run — with mock SourceManager
// ---------------------------------------------------------------------------

// mockUserConfigManager implements config.UserConfigManager for testing
type mockUserConfigManager struct {
	mock.Mock
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	args := m.Called()
	return args.Get(0).(config.Config), args.Error(1)
}

func (m *mockUserConfigManager) Save(c config.Config) error {
	args := m.Called(c)
	return args.Error(0)
}

func newTestSourceManager(t *testing.T) (*extensions.SourceManager, *mockUserConfigManager) {
	t.Helper()
	cfgMgr := &mockUserConfigManager{}
	container := ioc.NewNestedContainer(nil)
	sm := extensions.NewSourceManager(container, cfgMgr, nil)
	return sm, cfgMgr
}

func Test_ExtensionSourceListAction_Success(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.mysource", map[string]any{
		"name":     "mysource",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        buf,
	}
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "mysource")
}

func Test_ExtensionSourceListAction_LoadError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), fmt.Errorf("config broken"))

	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        &bytes.Buffer{},
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config broken")
}

func Test_ExtensionSourceListAction_TableFormat(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.test", map[string]any{
		"name":     "test",
		"type":     "file",
		"location": "/tmp/test",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.TableFormatter{},
		writer:        buf,
	}
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test")
}

// ---------------------------------------------------------------------------
// extensionSourceRemoveAction.Run — with mock SourceManager
// ---------------------------------------------------------------------------

func Test_ExtensionSourceRemoveAction_Success(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.mysource", map[string]any{
		"name":     "mysource",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)
	cfgMgr.On("Save", mock.Anything).Return(nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceRemoveAction{
		sourceManager: sm,
		console:       console,
		args:          []string{"mysource"},
	}
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "mysource")
}

func Test_ExtensionSourceRemoveAction_RemoveError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	// source not found when listing
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.other", map[string]any{
		"name":     "other",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceRemoveAction{
		sourceManager: sm,
		console:       console,
		args:          []string{"nonexistent"},
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// extensionSourceAddAction.Run — with mock SourceManager
// ---------------------------------------------------------------------------

func Test_ExtensionSourceAddAction_InvalidSourceType(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags:         &extensionSourceAddFlags{name: "bad", location: "somewhere", kind: "badkind"},
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// extensionSourceAddAction.Run — config load error during CreateSource
// ---------------------------------------------------------------------------

func Test_ExtensionSourceAddAction_EmptyNameError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags:         &extensionSourceAddFlags{name: "", location: "", kind: "file"},
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// tryAutoInstallForPartialNamespace — early returns
// ---------------------------------------------------------------------------

func Test_TryAutoInstall_NoAnnotation(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "root"}
	container := ioc.NewNestedContainer(nil)
	result := tryAutoInstallForPartialNamespace(context.Background(), container, cmd, nil)
	assert.False(t, result)
}

func Test_TryAutoInstall_HasSubcommand(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "azd"}
	child := &cobra.Command{Use: "deploy"}
	root.AddCommand(child)
	container := ioc.NewNestedContainer(nil)
	// The "deploy" command already exists as sub-command, so partial namespace shouldn't trigger
	result := tryAutoInstallForPartialNamespace(context.Background(), container, root, []string{"deploy"})
	assert.False(t, result)
}

// ---------------------------------------------------------------------------
// processHooks — empty hooks list
// ---------------------------------------------------------------------------

func Test_ProcessHooks_Empty(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", nil, hookContextProject, false)
	require.NoError(t, err)
}

func Test_ProcessHooks_EmptySlice(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", []*ext.HookConfig{}, hookContextProject, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// promptInitType tests
// ---------------------------------------------------------------------------

func Test_PromptInitType_FromApp(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

	result, err := promptInitType(console, context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, initType(initFromApp), result)
}

func Test_PromptInitType_Template(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)

	result, err := promptInitType(console, context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, initType(initAppTemplate), result)
}

func Test_PromptInitType_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(_ input.ConsoleOptions) (any, error) { return 0, fmt.Errorf("cancelled") })

	_, err := promptInitType(console, context.Background(), nil, nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// processHooks — with prepare error (invalid platform)
// ---------------------------------------------------------------------------

func Test_ProcessHooks_PrepareError(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	hooks := []*ext.HookConfig{
		{Run: "echo hello"},
	}
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{platform: "invalid"},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", hooks, hookContextProject, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

// ---------------------------------------------------------------------------
// Extension source list with no sources configured (triggers default source creation)
// ---------------------------------------------------------------------------

func Test_ExtensionSourceListAction_DefaultSource(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	// No "extension.sources" key → triggers default source creation
	cfgMgr.On("Load").Return(cfg, nil)
	cfgMgr.On("Save", mock.Anything).Return(nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        buf,
	}
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	// Default source "azd" should appear
	assert.Contains(t, buf.String(), "azd")
}

// ---------------------------------------------------------------------------
// Extension source add — file source that doesn't exist (validation error)
// ---------------------------------------------------------------------------

func Test_ExtensionSourceAddAction_FileNotFound(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags: &extensionSourceAddFlags{
			name:     "local",
			location: "/nonexistent/path/to/registry.json",
			kind:     "file",
		},
	}
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// selectDistinctExtension - single item (no prompt needed)
// ---------------------------------------------------------------------------

func Test_SelectDistinctExtension_Single(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "ext.one", DisplayName: "Ext One"},
	}
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{}
	result, err := selectDistinctExtension(context.Background(), console, "ext.one", exts, globalOpts)
	require.NoError(t, err)
	assert.Equal(t, "ext.one", result.Id)
}

func Test_SelectDistinctExtension_Empty(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{}
	_, err := selectDistinctExtension(context.Background(), console, "ext.missing", nil, globalOpts)
	require.Error(t, err)
}

func Test_SelectDistinctExtension_NoPrompt(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "a", DisplayName: "A", Source: "s1"},
		{Id: "b", DisplayName: "B", Source: "s2"},
	}
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{NoPrompt: true}
	_, err := selectDistinctExtension(context.Background(), console, "test.ext", exts, globalOpts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "found in multiple sources")
}

// ---------------------------------------------------------------------------
// checkForMatchingExtensions - partial coverage improvement
// ---------------------------------------------------------------------------

func Test_CheckForMatchingExtensions_EmptyRegistry(t *testing.T) {
	t.Parallel()
	// Cannot easily mock Source interface for checkForMatchingExtensions
	// without a real implementation. Test is a placeholder.
}

// ---------------------------------------------------------------------------
// container registerAction / resolveAction deeper tests
// ---------------------------------------------------------------------------

func Test_ResolveAction_WithNilMiddleware(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})
	_, err := resolveAction[*coverageTestAction](container, "test-action")
	require.Error(t, err) // not registered
}

type coverageTestAction struct{}

func (a *coverageTestAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// parseConfigValue — boundary/edge cases
// ---------------------------------------------------------------------------

func Test_ParseConfigValue_Object(t *testing.T) {
	t.Parallel()
	v := parseConfigValue(`{"key": "value"}`)
	m, ok := v.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value", m["key"])
}

func Test_ParseConfigValue_Array(t *testing.T) {
	t.Parallel()
	v := parseConfigValue(`[1, 2, 3]`)
	arr, ok := v.([]any)
	require.True(t, ok)
	assert.Len(t, arr, 3)
}

func Test_ParseConfigValue_Bool(t *testing.T) {
	t.Parallel()
	v := parseConfigValue("true")
	b, ok := v.(bool)
	require.True(t, ok)
	assert.True(t, b)
}

func Test_ParseConfigValue_Number(t *testing.T) {
	t.Parallel()
	v := parseConfigValue("42")
	f, ok := v.(float64)
	require.True(t, ok)
	assert.InDelta(t, 42.0, f, 0.001)
}

func Test_ParseConfigValue_String(t *testing.T) {
	t.Parallel()
	v := parseConfigValue("hello world")
	s, ok := v.(string)
	require.True(t, ok)
	assert.Equal(t, "hello world", s)
}

// ---------------------------------------------------------------------------
// checkNamespaceConflict — additional scenarios
// ---------------------------------------------------------------------------

func Test_CheckNamespaceConflict_NoConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{}
	err := checkNamespaceConflict("new.ext", "foo", installed)
	require.NoError(t, err)
}

func Test_CheckNamespaceConflict_WithConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"existing.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("new.ext", "foo", installed)
	require.Error(t, err)
}

func Test_CheckNamespaceConflict_EmptyNs_NoConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"existing.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("new.ext", "", installed)
	require.NoError(t, err) // empty namespace => no conflict
}

func Test_CheckNamespaceConflict_SkipSelf(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"self.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("self.ext", "foo", installed)
	require.NoError(t, err) // skips self
}

// (end of file)
