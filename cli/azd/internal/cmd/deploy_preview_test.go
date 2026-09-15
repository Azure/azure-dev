// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Only target resolution and preview are implemented. Calling any pipeline or resource method panics.
type deployPreviewServiceManager struct {
	project.ServiceManager
	mock.Mock
}

func (m *deployPreviewServiceManager) GetServiceTarget(
	ctx context.Context,
	service *project.ServiceConfig,
) (project.ServiceTarget, error) {
	args := m.Called(ctx, service)
	target, _ := args.Get(0).(project.ServiceTarget)
	return target, args.Error(1)
}

type deployPreviewTarget struct {
	project.ServiceTarget
	mock.Mock
}

func (m *deployPreviewTarget) Preview(
	ctx context.Context,
	service *project.ServiceConfig,
) (*project.ServiceDeployPreviewResult, error) {
	args := m.Called(ctx, service)
	result, _ := args.Get(0).(*project.ServiceDeployPreviewResult)
	return result, args.Error(1)
}

type deployWithoutPreviewTarget struct {
	project.ServiceTarget
}

const deployPreviewProject = `
name: preview-project
services:
  web:
    project: cmd
    language: js
    host: custom
  api:
    project: pkg
    language: dotnet
    host: custom
    condition: ${API_ENABLED}
hooks:
  predeploy:
    run: do-not-run
`

func newDeployPreviewAction(t *testing.T, args ...string) (*DeployAction, *deployPreviewServiceManager, *bytes.Buffer) {
	t.Helper()

	projectConfig, err := project.Parse(t.Context(), deployPreviewProject)
	require.NoError(t, err)
	wd, err := os.Getwd()
	require.NoError(t, err)
	projectConfig.Path = filepath.Clean(filepath.Join(wd, "..", ".."))

	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{NoPrompt: true})
	require.NoError(t, cmd.ParseFlags(append([]string{"--preview"}, args...)))

	manager := &deployPreviewServiceManager{}
	manager.Test(t)
	envManager := &mockenv.MockEnvManager{}
	envManager.Test(t)
	writer := &bytes.Buffer{}
	action := &DeployAction{
		flags:         flags,
		args:          cmd.Flags().Args(),
		projectConfig: projectConfig,
		azdCtx:        azdcontext.NewAzdContextWithDirectory(projectConfig.Path),
		env: environment.NewWithValues("unprovisioned", map[string]string{
			"AZURE_AI_PROJECT_ENDPOINT": "https://example.services.ai.azure.com/api/projects/preview",
			"AZURE_SUBSCRIPTION_ID":     "",
			"API_ENABLED":               "true",
		}),
		envManager:              envManager,
		serviceTargetResolver:   manager,
		declaredServiceResolver: project.NewImportManager(nil),
		console:                 mockinput.NewMockConsole(),
		formatter:               &output.NoneFormatter{},
		writer:                  writer,
	}
	initialValues := action.env.Dotenv()
	initialConfig, err := json.Marshal(action.env.Config.Raw())
	require.NoError(t, err)
	t.Cleanup(func() {
		manager.AssertExpectations(t)
		require.Empty(t, envManager.Calls, "preview must not load, save, reload, or invalidate environment state")
		require.Equal(t, initialValues, action.env.Dotenv(), "preview must not update environment values")
		currentConfig, err := json.Marshal(action.env.Config.Raw())
		require.NoError(t, err)
		require.JSONEq(t, string(initialConfig), string(currentConfig), "preview must not update environment config")
	})
	return action, manager, writer
}

func expectDeployPreview(
	t *testing.T,
	action *DeployAction,
	manager *deployPreviewServiceManager,
	name string,
	result *project.ServiceDeployPreviewResult,
) *deployPreviewTarget {
	t.Helper()
	service := action.projectConfig.Services[name]
	target := &deployPreviewTarget{}
	target.Test(t)
	manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
	target.On("Preview", mock.Anything, service).Return(result, nil).Once()
	t.Cleanup(func() { target.AssertExpectations(t) })
	return target
}

func TestDeployPreviewFlags(t *testing.T) {
	t.Parallel()
	cmd := NewDeployCmd()
	flags := NewDeployFlags(cmd, &internal.GlobalCommandOptions{})
	require.NotNil(t, cmd.Flags().Lookup("preview"))
	require.False(t, flags.Preview)
	require.True(t, cmd.Flags().Lookup("service").Hidden)
	require.NoError(t, cmd.ParseFlags([]string{"--preview"}))
	require.True(t, flags.Preview)

	sharedFlags := &DeployFlags{}
	sharedSet := pflag.NewFlagSet("up", pflag.ContinueOnError)
	sharedFlags.BindNonCommon(sharedSet, &internal.GlobalCommandOptions{})
	require.Nil(t, sharedSet.Lookup("preview"), "azd up must not inherit --preview")
	commonSet := pflag.NewFlagSet("common", pflag.ContinueOnError)
	sharedFlags.bindCommon(commonSet, &internal.GlobalCommandOptions{})
	require.Nil(t, commonSet.Lookup("preview"))
}

func TestDeployPreviewReadOnlyUnprovisionedEnvironment(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "api")
	require.Empty(t, action.env.GetSubscriptionId())
	require.NotEmpty(t, action.env.Getenv("AZURE_AI_PROJECT_ENDPOINT"))
	require.NoError(t, action.projectConfig.AddHandler(
		t.Context(), project.ProjectEventDeploy,
		func(context.Context, project.ProjectLifecycleEventArgs) error {
			t.Error("preview must not invoke project deployment hooks")
			return nil
		},
	))
	require.NoError(t, action.projectConfig.Services["api"].AddHandler(
		t.Context(), project.ServiceEventDeploy,
		func(context.Context, project.ServiceLifecycleEventArgs) error {
			t.Error("preview must not invoke service deployment hooks")
			return nil
		},
	))
	expectDeployPreview(t, action, manager, "api", &project.ServiceDeployPreviewResult{
		Message: "Planned api deployment\n",
		Data:    map[string]any{"service": "api"},
	})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.Message, "preview must not report a successful deployment")
	require.Equal(t, "Planned api deployment\n", writer.String())
	require.Empty(t, action.console.(*mockinput.MockConsole).Output())
	require.Nil(t, action.progressTracker)
}

func TestDeployPreviewSelection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "All", args: []string{"--all"}, want: []string{"api", "web"}},
		{name: "Positional", args: []string{"web"}, want: []string{"web"}},
		{name: "HiddenService", args: []string{"--service", "api"}, want: []string{"api"}},
		{name: "PositionalWins", args: []string{"api", "--service", "web"}, want: []string{"api"}},
		{
			name: "PositionalOverridesInvalidHiddenService",
			args: []string{"api", "--service", "missing"},
			want: []string{"api"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, writer := newDeployPreviewAction(t, tt.args...)
			for _, name := range tt.want {
				expectDeployPreview(t, action, manager, name, &project.ServiceDeployPreviewResult{Message: name})
			}
			_, err := action.Run(t.Context())
			require.NoError(t, err)
			require.Equal(t, strings.Join(tt.want, "\n")+"\n", writer.String())
			require.Len(t, manager.Calls, len(tt.want))
		})
	}
}

func TestDeployPreviewDirectorySelection(t *testing.T) {
	tests := []struct {
		name   string
		dir    string
		want   []string
		all    bool
		errMsg string
	}{
		{name: "Project", want: []string{"api", "web"}},
		{name: "Service", dir: "pkg", want: []string{"api"}},
		{name: "OtherService", dir: "cmd", want: []string{"web"}},
		{name: "AllFromServiceDirectory", dir: "pkg", all: true, want: []string{"api", "web"}},
		{name: "UnrelatedDirectory", dir: "internal", errMsg: "not a project or declared service directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args []string
			if tt.all {
				args = []string{"--all"}
			}
			action, manager, _ := newDeployPreviewAction(t, args...)
			t.Chdir(filepath.Join(action.projectConfig.Path, tt.dir))
			for _, name := range tt.want {
				expectDeployPreview(t, action, manager, name, &project.ServiceDeployPreviewResult{})
			}
			_, err := action.Run(t.Context())
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)
				require.Empty(t, manager.Calls)
				return
			}
			require.NoError(t, err)
			require.Len(t, manager.Calls, len(tt.want))
		})
	}
}

func TestDeployPreviewDependencyOrder(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "--all")
	action.projectConfig.Services["api"].Uses = []string{"web"}
	for _, name := range []string{"api", "web"} {
		expectDeployPreview(t, action, manager, name, &project.ServiceDeployPreviewResult{Message: name})
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, "web\napi\n", writer.String())
	require.Same(t, action.projectConfig.Services["web"], manager.Calls[0].Arguments.Get(1))
	require.Same(t, action.projectConfig.Services["api"], manager.Calls[1].Arguments.Get(1))
}

func TestDeployPreviewConditions(t *testing.T) {
	tests := []struct {
		name      string
		condition string
		args      []string
		want      []string
		errMsg    string
	}{
		{name: "DisabledOmitted", condition: "false", args: []string{"--all"}, want: []string{"web"}},
		{name: "DisabledTarget", condition: "false", args: []string{"api"}, errMsg: "deployment condition"},
		{name: "MalformedCondition", condition: "${INVALID", args: []string{"--all"}, errMsg: "malformed"},
		{name: "OtherTargetIgnoresCondition", condition: "${INVALID", args: []string{"web"}, want: []string{"web"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, _ := newDeployPreviewAction(t, tt.args...)
			action.projectConfig.Services["api"].Condition = osutil.NewExpandableString(tt.condition)
			for _, name := range tt.want {
				expectDeployPreview(t, action, manager, name, &project.ServiceDeployPreviewResult{})
			}
			_, err := action.Run(t.Context())
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)
				require.Empty(t, manager.Calls)
				return
			}
			require.NoError(t, err)
			require.Len(t, manager.Calls, len(tt.want))
		})
	}
}

func TestDeployPreviewRejectsConflictingFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "Package", args: []string{"api", "--from-package", "image:tag"}, want: "--preview and --from-package"},
		{name: "EmptyPackage", args: []string{"api", "--from-package="}, want: "--preview and --from-package"},
		{name: "AllAndPositional", args: []string{"api", "--all"}, want: "--all and <service>"},
		{name: "AllAndHiddenService", args: []string{"--all", "--service", "api"}, want: "--all and <service>"},
		{name: "InvalidTimeout", args: []string{"api", "--timeout", "0"}, want: "invalid value for --timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, writer := newDeployPreviewAction(t, tt.args...)
			_, err := action.Run(t.Context())
			require.ErrorContains(t, err, tt.want)
			require.Empty(t, manager.Calls)
			require.Empty(t, writer.String())
		})
	}
}

func TestDeployPreviewDoesNotImportGeneratedServices(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		generated bool
	}{
		{name: "GeneratedName", args: []string{"generated-api"}},
		{name: "PreviouslyImportedService", args: []string{"api"}, generated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, _ := newDeployPreviewAction(t, tt.args...)
			if tt.generated {
				action.projectConfig.Services["api"].DotNetContainerApp = &project.DotNetContainerAppOptions{}
			}
			_, err := action.Run(t.Context())
			require.ErrorContains(t, err, "generated")
			require.Empty(t, manager.Calls)
		})
	}
}

func TestDeployPreviewProviderFailures(t *testing.T) {
	providerErr := errors.New("read-only provider request failed")
	tests := []struct {
		name          string
		target        project.ServiceTarget
		resolutionErr error
		previewErr    error
		want          string
	}{
		{name: "Unsupported", target: &deployWithoutPreviewTarget{}, want: "does not support deployment preview"},
		{name: "NilTarget", want: "does not support deployment preview"},
		{name: "ResolutionError", resolutionErr: providerErr, want: "resolving service host for preview"},
		{name: "ProviderError", target: &deployPreviewTarget{}, previewErr: providerErr, want: "previewing deployment"},
		{name: "NilResult", target: &deployPreviewTarget{}, want: "provider returned no result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, writer := newDeployPreviewAction(t, "api")
			service := action.projectConfig.Services["api"]
			manager.On("GetServiceTarget", mock.Anything, service).Return(tt.target, tt.resolutionErr).Once()
			if target, ok := tt.target.(*deployPreviewTarget); ok {
				target.On("Preview", mock.Anything, service).Return(nil, tt.previewErr).Once()
				t.Cleanup(func() { target.AssertExpectations(t) })
			}
			_, err := action.Run(t.Context())
			require.ErrorContains(t, err, tt.want)
			if tt.resolutionErr != nil || tt.previewErr != nil {
				require.ErrorIs(t, err, providerErr)
			}
			require.Empty(t, writer.String())
		})
	}
}

func TestDeployPreviewJSONOnly(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "--all")
	action.formatter = &output.JsonFormatter{}
	for _, name := range []string{"api", "web"} {
		expectDeployPreview(t, action, manager, name, &project.ServiceDeployPreviewResult{
			Message: "Human preview text must not appear in JSON",
			Data:    map[string]any{"service": name, "changes": []any{"create", "update"}},
		})
	}
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result.Message)
	require.NotContains(t, writer.String(), "Human preview")
	require.NotContains(t, writer.String(), `"Message"`)
	require.NotContains(t, writer.String(), `"message"`)
	require.Empty(t, action.console.(*mockinput.MockConsole).Output())

	decoder := json.NewDecoder(writer)
	var parsed struct {
		Timestamp time.Time `json:"timestamp"`
		Services  map[string]struct {
			Data map[string]any `json:"data"`
		} `json:"services"`
	}
	require.NoError(t, decoder.Decode(&parsed))
	require.ErrorIs(t, decoder.Decode(&struct{}{}), io.EOF, "output must contain exactly one JSON value")
	require.WithinDuration(t, time.Now(), parsed.Timestamp, time.Second)
	require.Len(t, parsed.Services, 2)
	for _, name := range []string{"api", "web"} {
		require.Equal(t, name, parsed.Services[name].Data["service"])
		require.Equal(t, []any{"create", "update"}, parsed.Services[name].Data["changes"])
	}
}

func TestDeployPreviewEmptySelectionJSON(t *testing.T) {
	for _, name := range []string{"EmptyProject", "AllServicesDisabled"} {
		t.Run(name, func(t *testing.T) {
			action, manager, writer := newDeployPreviewAction(t, "--all")
			if name == "EmptyProject" {
				action.projectConfig.Services = map[string]*project.ServiceConfig{}
			} else {
				for _, service := range action.projectConfig.Services {
					service.Condition = osutil.NewExpandableString("false")
				}
			}
			action.formatter = &output.JsonFormatter{}
			_, err := action.Run(t.Context())
			require.NoError(t, err)
			require.Contains(t, writer.String(), `"services": {}`)
			require.Empty(t, manager.Calls)
		})
	}
}

func TestDeployPreviewTimeout(t *testing.T) {
	action, manager, _ := newDeployPreviewAction(t, "api", "--timeout", "1")
	target := &deployPreviewTarget{}
	target.Test(t)
	service := action.projectConfig.Services["api"]
	manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
	start := time.Now()
	target.On("Preview", mock.Anything, service).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, start.Add(time.Second), deadline, 200*time.Millisecond)
		<-ctx.Done()
	}).Return(nil, context.DeadlineExceeded).Once()

	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "previewing deployment of service 'api'")
	target.AssertExpectations(t)
}

func TestDeployPreviewTimeoutEnvironmentAndFlagPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		timeout time.Duration
		wantErr bool
	}{
		{name: "Environment", env: "23", timeout: 23 * time.Second},
		{name: "Flag", env: "23", args: []string{"--timeout", "45"}, timeout: 45 * time.Second},
		{name: "InvalidEnvironment", env: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_DEPLOY_TIMEOUT", tt.env)
			action, manager, _ := newDeployPreviewAction(t, append([]string{"api"}, tt.args...)...)
			if tt.wantErr {
				_, err := action.Run(t.Context())
				require.ErrorContains(t, err, "invalid AZD_DEPLOY_TIMEOUT")
				require.Empty(t, manager.Calls)
				return
			}
			target := expectDeployPreview(t, action, manager, "api", &project.ServiceDeployPreviewResult{})
			start := time.Now()
			target.ExpectedCalls[0].Run(func(args mock.Arguments) {
				deadline, ok := args.Get(0).(context.Context).Deadline()
				require.True(t, ok)
				require.WithinDuration(t, start.Add(tt.timeout), deadline, time.Second)
			})
			_, err := action.Run(t.Context())
			require.NoError(t, err)
		})
	}
}

func TestDeployPreviewCanceledContext(t *testing.T) {
	action, manager, writer := newDeployPreviewAction(t, "api")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := action.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, manager.Calls)
	require.Empty(t, writer.String())
}

func TestDeployPreviewOutputFailure(t *testing.T) {
	for _, formatter := range []output.Formatter{&output.NoneFormatter{}, &output.JsonFormatter{}} {
		t.Run(string(formatter.Kind()), func(t *testing.T) {
			action, manager, _ := newDeployPreviewAction(t, "api")
			action.formatter = formatter
			action.writer = previewErrorWriter{}
			expectDeployPreview(t, action, manager, "api", &project.ServiceDeployPreviewResult{Message: "Plan"})
			_, err := action.Run(t.Context())
			require.ErrorIs(t, err, io.ErrClosedPipe)
			require.ErrorContains(t, err, "deployment preview could not be displayed")
		})
	}
}

type previewErrorWriter struct{}

func (previewErrorWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
