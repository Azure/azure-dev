// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktools"
)

// registerHookExecutors delegates to the shared test helper in test/mocks/mocktools.
func registerHookExecutors(mockCtx *mocks.MockContext) {
	mocktools.RegisterHookExecutors(mockCtx)
}

func Test_HooksRunAction_RunsLayerHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerHookExecutors(mockContext)
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	projectPath := t.TempDir()
	absoluteLayerPath := filepath.Join(t.TempDir(), "shared")

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: string(language.HookKindBash),
							Run:   "echo core",
						}},
					},
				},
				{
					Name: "shared",
					Path: absoluteLayerPath,
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: string(language.HookKindBash),
							Run:   "echo shared",
						}},
					},
				},
			},
		},
	}

	var gotCwds []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		gotCwds = append(gotCwds, args.Cwd)
		return exec.NewRunResult(0, "", ""), nil
	})

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	result, err := action.Run(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, []string{
		filepath.Join(projectPath, "infra/core"),
		absoluteLayerPath,
	}, gotCwds)
}

func Test_HooksRunAction_FiltersLayerHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerHookExecutors(mockContext)
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	projectPath := t.TempDir()

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: string(language.HookKindBash),
							Run:   "echo core",
						}},
					},
				},
				{
					Name: "shared",
					Path: "infra/shared",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: string(language.HookKindBash),
							Run:   "echo shared",
						}},
					},
				},
			},
		},
	}

	var gotCwds []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		gotCwds = append(gotCwds, args.Cwd)
		return exec.NewRunResult(0, "", ""), nil
	})

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{layer: "shared"},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	result, err := action.Run(*mockContext.Context)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, []string{
		filepath.Join(projectPath, "infra/shared"),
	}, gotCwds)
}

func Test_HooksRunAction_SetsTelemetryTypeForLayer(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	registerHookExecutors(mockContext)
	env := environment.NewWithValues("test", nil)
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	t.Cleanup(func() {
		tracing.SetUsageAttributes()
	})
	tracing.SetUsageAttributes()

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     t.TempDir(),
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: "infra/core",
					Hooks: provisioning.HooksConfig{
						"preprovision": {{
							Shell: string(language.HookKindBash),
							Run:   "echo core",
						}},
					},
				},
			},
		},
	}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).Respond(exec.NewRunResult(0, "", ""))

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{layer: "core"},
		args:           []string{"preprovision"},
		serviceLocator: mockContext.Container,
	}

	_, err := action.Run(*mockContext.Context)
	require.NoError(t, err)

	var hookType string
	for _, attr := range tracing.GetUsageAttributes() {
		if attr.Key == fields.HooksTypeKey.Key {
			hookType = attr.Value.AsString()
			break
		}
	}

	require.Equal(t, "layer", hookType)
}

func Test_HooksRunAction_HookNameTelemetry(t *testing.T) {
	tests := []struct {
		name          string
		hookName      string
		configured    bool
		scope         string
		wantName      bool
		wantExecution bool
	}{
		{"ProjectHook", "predeploy", true, "project", true, true},
		{"LayerHook", "preprovision", true, "layer", true, true},
		{"ServiceHook", "postdeploy", true, "service", true, true},
		{"InfraAlias", "preinfracreate", true, "project", true, true},
		{"KnownButUnmatched", "predeploy", false, "project", false, false},
		{"UnknownAndUnmatched", "private-customer-hook", false, "project", false, false},
		{"UnknownButConfigured", "private-customer-hook", true, "project", false, true},
		{"SkippedLayer", "preprovision", true, "skipped-layer", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracing.ResetUsageAttributesForTest()
			t.Cleanup(tracing.ResetUsageAttributesForTest)
			recorder := tracetest.NewSpanRecorder()
			provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))

			mockContext := mocks.NewMockContext(t.Context())
			registerHookExecutors(mockContext)
			env := environment.NewWithValues("test", nil)
			envManager := &mockenv.MockEnvManager{}
			envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)
			config := &project.ProjectConfig{
				Name: "test", Path: t.TempDir(), Services: map[string]*project.ServiceConfig{},
			}
			flags := &hooksRunFlags{}
			hooks := project.HooksConfig{}
			if tt.configured {
				hooks[tt.hookName] = []*ext.HookConfig{{
					Shell: string(language.HookKindBash), Run: "echo hook",
				}}
			}
			switch tt.scope {
			case "layer", "skipped-layer":
				config.Infra.Layers = []provisioning.Options{{Name: "core", Path: ".", Hooks: hooks}}
				flags.layer = "core"
				if tt.scope == "skipped-layer" {
					config.Infra.Layers = append(config.Infra.Layers, provisioning.Options{Name: "other", Path: "."})
					flags.layer = "other"
				}
			case "service":
				config.Services["api"] = &project.ServiceConfig{
					Name: "api", Project: config, RelativePath: ".", Hooks: hooks,
				}
				flags.service = "api"
			default:
				config.Hooks = hooks
			}
			executions := 0
			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				executions++
				return exec.NewRunResult(0, "", ""), nil
			})
			action := &hooksRunAction{
				projectConfig: config, env: env, envManager: envManager,
				importManager: project.NewImportManager(nil), commandRunner: mockContext.CommandRunner,
				console: mockContext.Console, flags: flags, args: []string{tt.hookName},
				serviceLocator: mockContext.Container,
			}
			ctx, span := provider.Tracer("test").Start(*mockContext.Context, "cmd.hooks.run")
			_, err := action.Run(ctx)
			span.SetAttributes(tracing.GetUsageAttributes()...)
			span.End()
			require.NoError(t, err)
			assert.Equal(t, tt.wantExecution, executions > 0)

			foundName := false
			require.NotEmpty(t, recorder.Ended())
			for _, recorded := range recorder.Ended() {
				for _, attr := range recorded.Attributes() {
					if attr.Key == fields.HooksNameKey.Key {
						foundName = true
						assert.True(t, tt.wantName, "unexpected hook-name attribute on %s", recorded.Name())
						assert.Equal(t, tt.hookName, attr.Value.AsString())
					}
					if !tt.wantName {
						assert.NotContains(t, attr.Value.String(), tt.hookName)
						assert.NotContains(t, attr.Value.String(), fields.CaseInsensitiveHash(tt.hookName))
					}
				}
			}
			assert.Equal(t, tt.wantName, foundName)
		})
	}
}

func Test_HooksRunAction_RejectsServiceAndLayerTogether(t *testing.T) {
	action := &hooksRunAction{
		env:   environment.NewWithValues("test", nil),
		flags: &hooksRunFlags{service: "api", layer: "core"},
		args:  []string{"preprovision"},
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "--service and --layer cannot be used together")
}

func Test_HooksRunAction_ValidatesLayerHooksRelativeToLayerPath(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.NewWithValues("test", nil)

	projectPath := t.TempDir()
	layerScriptPath := filepath.Join(projectPath, "infra", "core", "scripts", "preprovision.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(layerScriptPath), 0o755))
	require.NoError(t, os.WriteFile(layerScriptPath, []byte("echo pre"), 0o600))

	layerHook := &ext.HookConfig{
		Run: filepath.Join("scripts", "preprovision.sh"),
	}

	projectConfig := &project.ProjectConfig{
		Name:     "test",
		Path:     projectPath,
		Services: map[string]*project.ServiceConfig{},
		Infra: provisioning.Options{
			Layers: []provisioning.Options{
				{
					Name: "core",
					Path: filepath.Join("infra", "core"),
					Hooks: provisioning.HooksConfig{
						"preprovision": {layerHook},
					},
				},
			},
		},
	}

	action := &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		importManager:  project.NewImportManager(nil),
		commandRunner:  mockContext.CommandRunner,
		console:        mockContext.Console,
		flags:          &hooksRunFlags{},
		serviceLocator: mockContext.Container,
	}

	err := action.validateAndWarnHooks(*mockContext.Context)
	require.NoError(t, err)
	require.False(t, layerHook.IsUsingDefaultShell())
	// validate() infers language from the .sh file extension
	require.Equal(t, language.HookKindBash, layerHook.Kind)
}

func Test_NewHooksRunAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &hooksRunFlags{}
	console := mockinput.NewMockConsole()
	args := []string{"pre-build"}
	a := newHooksRunAction(nil, nil, nil, nil, nil, console, flags, args, nil)
	ha := a.(*hooksRunAction)
	require.Same(t, flags, ha.flags)
	require.Equal(t, args, ha.args)
}

func Test_NewHooksRunAction(t *testing.T) {
	t.Parallel()
	action := newHooksRunAction(
		&project.ProjectConfig{},
		nil, // importManager
		environment.NewWithValues("test", nil),
		nil, // envManager
		nil, // commandRunner
		mockinput.NewMockConsole(),
		&hooksRunFlags{},
		[]string{"pre-provision"},
		ioc.NewNestedContainer(nil),
	)
	require.NotNil(t, action)
}

func Test_ProcessHooks_SkipTrue(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	hooks := []*ext.HookConfig{
		{Run: "echo hello"},
	}
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	// skip=true should skip actual execution
	err := action.processHooks(*mockCtx.Context, "", "prehook", hooks, hookContextProject, true)
	require.NoError(t, err)
}

func Test_ProcessHooks_NilHooks(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", nil, hookContextProject, false)
	require.NoError(t, err)
}

func Test_ProcessHooks_SkipWithHooks(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	hra := &hooksRunAction{
		console: console,
	}

	hooks := []*ext.HookConfig{
		{Run: "echo hello"},
		{Run: "echo world"},
	}

	err := hra.processHooks(t.Context(), "/tmp", "prebuild", hooks, hookContextService, true)
	require.NoError(t, err)
}

func Test_ProcessHooks_EmptyHooks(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	hra := &hooksRunAction{
		console: console,
	}

	err := hra.processHooks(t.Context(), "/tmp", "prebuild", nil, hookContextProject, false)
	require.NoError(t, err)
}

func Test_PrepareHook_WindowsPlatform(t *testing.T) {
	t.Parallel()
	hra := &hooksRunAction{
		flags: &hooksRunFlags{platform: "windows"},
	}
	winHook := &ext.HookConfig{Run: "echo win"}
	hook := &ext.HookConfig{Run: "echo default", Windows: winHook}
	err := hra.prepareHook("prehook", hook)
	require.NoError(t, err)
	require.Equal(t, "echo win", hook.Run)
}

func Test_PrepareHook_PosixPlatform(t *testing.T) {
	t.Parallel()
	hra := &hooksRunAction{
		flags: &hooksRunFlags{platform: "posix"},
	}
	posixHook := &ext.HookConfig{Run: "echo posix"}
	hook := &ext.HookConfig{Run: "echo default", Posix: posixHook}
	err := hra.prepareHook("prehook", hook)
	require.NoError(t, err)
	require.Equal(t, "echo posix", hook.Run)
}

func Test_PrepareHook_WindowsMissing(t *testing.T) {
	t.Parallel()
	hra := &hooksRunAction{
		flags: &hooksRunFlags{platform: "windows"},
	}
	hook := &ext.HookConfig{Run: "echo default"}
	err := hra.prepareHook("prehook", hook)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Windows")
}

func Test_PrepareHook_PosixMissing(t *testing.T) {
	t.Parallel()
	hra := &hooksRunAction{
		flags: &hooksRunFlags{platform: "posix"},
	}
	hook := &ext.HookConfig{Run: "echo default"}
	err := hra.prepareHook("prehook", hook)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Posix")
}

func Test_NewHooksRunFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newHooksRunFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewHooksRunCmd(t *testing.T) {
	t.Parallel()
	cmd := newHooksRunCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "run")
}

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
	assert.Contains(t, err.Error(), "not valid")
}

func Test_ProcessHooks_Empty(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", nil, hookContextProject, false)
	require.NoError(t, err)
}

func Test_ProcessHooks_EmptySlice(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", []*ext.HookConfig{}, hookContextProject, false)
	require.NoError(t, err)
}

func Test_ProcessHooks_PrepareError(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
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
