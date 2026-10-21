// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const listAlphaOutput = `Name: agents.long_running
Description: Start background Responses invocations.
Status: %s

Name: agents.managed_harness
Description: Use a Microsoft Copilot harness with prompt agents.
Status: %s

Name: agents.voice
Description: Use managed voice agents and hosted voice bridges.
Status: %s
`

func TestListAlphaCommandSources(t *testing.T) {
	for _, source := range []string{"process", "dotenv", "config", "service"} {
		for mask := range 8 {
			t.Run(fmt.Sprintf("%s/%03b", source, mask), func(t *testing.T) {
				clearPreviewProcess(t)
				agents := map[string]any{"hosted": "on"}
				alpha := map[string]any{"agents": agents, "all": "on", "core-only": "on"}
				config := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
					"web": {Host: "appservice", Environment: map[string]string{}},
					"api": {Host: "containerapp", Environment: map[string]string{}},
				}}
				var values []*azdext.KeyValue
				var statuses []string
				for i, feature := range featureflags.List() {
					enabled := mask&(1<<i) != 0
					value := fmt.Sprint(enabled)
					agents[strings.TrimPrefix(feature.ID, "agents.")] = "off"
					config.Services["api"].Environment[feature.EnvVar] = "false"
					switch source {
					case "process":
						t.Setenv(feature.EnvVar, value)
					case "dotenv":
						values = append(values, &azdext.KeyValue{Key: feature.EnvVar, Value: value})
					case "config":
						if enabled {
							agents[strings.TrimPrefix(feature.ID, "agents.")] = "On"
						}
					case "service":
						config.Services["web"].Environment[feature.EnvVar] = value
					}
					if enabled {
						statuses = append(statuses, "On")
					} else {
						statuses = append(statuses, "Off")
					}
				}
				_, env := startPreviewCommandServer(t, alpha, config, nil)
				env.mu.Lock()
				env.values = values
				env.mu.Unlock()

				out, err := executeListAlphaCommand(t, "-e", "selected-env", "--no-prompt")

				require.NoError(t, err)
				require.Equal(t, fmt.Sprintf(listAlphaOutput, statuses[0], statuses[1], statuses[2]), out)
				env.mu.Lock()
				require.Equal(t, "selected-env", env.selected)
				env.mu.Unlock()
			})
		}
	}
}

func TestListAlphaCommandAllFeatureConfig(t *testing.T) {
	clearPreviewProcess(t)
	startPreviewCommandServer(t, map[string]any{"agents": map[string]any{
		"long_running": "oN", "managed_harness": "ON", "voice": "On",
	}}, nil, nil)

	out, err := executeListAlphaCommand(t, "--output", "default")

	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf(listAlphaOutput, "On", "On", "On"), out)
	t.Logf("Captured list-alpha output:\n%s", out)
}

func TestListAlphaCommandGlobalConfigIgnored(t *testing.T) {
	for _, value := range []any{"on", "ON", "On", "oN", "off", "", true, nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			clearPreviewProcess(t)
			startPreviewCommandServer(t, map[string]any{"all": value}, nil, nil)

			out, err := executeListAlphaCommand(t)

			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf(listAlphaOutput, "Off", "Off", "Off"), out)
			t.Logf("Captured list-alpha output with alpha.all=%v:\n%s", value, out)
		})
	}
}

func TestListAlphaCommandEnvironmentSelection(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"host selection", nil, "host-selected"},
		{"explicit selection", []string{"-e", "selected-env"}, "selected-env"},
		{"explicit empty", []string{"--environment="}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Setenv("AZD_ENVIRONMENT", "host-selected")
			var calls atomic.Int32
			startPreviewCommandServer(t, map[string]any{"all": "on"}, nil, nil, grpc.UnaryInterceptor(func(
				ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
			) (any, error) {
				if strings.HasSuffix(info.FullMethod, "EnvironmentService/GetValues") {
					selected, ok := request.(*azdext.GetEnvironmentRequest)
					if !ok || selected.GetName() != tt.want {
						return nil, status.Error(codes.InvalidArgument, "unexpected environment selection")
					}
				}
				calls.Add(1)
				return handler(ctx, request)
			}))

			out, err := executeListAlphaCommand(t, tt.args...)

			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf(listAlphaOutput, "Off", "Off", "Off"), out)
			require.EqualValues(t, 9, calls.Load())
		})
	}
}

func TestListAlphaCommandOptionalScopes(t *testing.T) {
	for _, scope := range []struct {
		name   string
		method string
		cause  error
	}{
		{"no project", "ProjectService/Get", azdcontext.ErrNoProject},
		{"no default environment", "EnvironmentService/GetValues", environment.ErrDefaultEnvironmentNotFound},
	} {
		for _, positive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", scope.name, positive), func(t *testing.T) {
				clearPreviewProcess(t)
				value := "off"
				if positive {
					value = "on"
				}
				t.Setenv(featureflags.ManagedHarness.EnvVar, fmt.Sprint(positive))
				startPreviewCommandServer(t,
					map[string]any{"all": "on", "agents": map[string]any{"long_running": value}},
					&azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
						"web": {Host: "appservice", Environment: map[string]string{
							featureflags.Voice.EnvVar: fmt.Sprint(positive),
						}},
					}}, nil, grpc.UnaryInterceptor(func(
						ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
					) (any, error) {
						if strings.HasSuffix(info.FullMethod, scope.method) {
							return nil, status.Error(codes.Unknown, scope.cause.Error())
						}
						return handler(ctx, request)
					}))

				out, err := executeListAlphaCommand(t, "--no-prompt")

				require.NoError(t, err)
				longRunning, managedHarness, voice := "Off", "Off", "Off"
				if positive {
					longRunning, managedHarness = "On", "On"
					if scope.name == "no default environment" {
						voice = "On"
					}
				}
				require.Equal(t, fmt.Sprintf(listAlphaOutput, longRunning, managedHarness, voice), out)
			})
		}
	}
}

func TestListAlphaCommandSourceErrorsHaveNoPartialOutput(t *testing.T) {
	for _, tt := range []struct {
		name        string
		method      string
		cause       error
		environment string
		want        string
	}{
		{
			name: "config transport", method: "UserConfigService/Get",
			cause: status.Error(codes.Unavailable, "config unavailable"), want: "reading user config",
		},
		{name: "malformed config", method: "UserConfigService/Get", want: "decoding alpha user config"},
		{
			name: "project read", method: "ProjectService/Get",
			cause: status.Error(codes.PermissionDenied, "project unreadable"), want: "loading project",
		},
		{
			name: "environment transport", method: "EnvironmentService/GetValues",
			cause: status.Error(codes.Unavailable, "environment unavailable"), want: "reading azd environment",
		},
		{
			name: "invalid explicit environment", method: "EnvironmentService/GetValues",
			cause:       status.Error(codes.Unknown, environment.ErrNotFound.Error()),
			environment: "missing", want: `reading azd environment "missing"`,
		},
		{
			name: "explicit environment without project", method: "ProjectService/Get",
			cause:       status.Error(codes.Unknown, azdcontext.ErrNoProject.Error()),
			environment: "selected", want: "loading project",
		},
		{
			name: "explicit no default sentinel", method: "EnvironmentService/GetValues",
			cause:       status.Error(codes.Unknown, environment.ErrDefaultEnvironmentNotFound.Error()),
			environment: "selected", want: `reading azd environment "selected"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearPreviewProcess(t)
			for _, feature := range featureflags.List() {
				t.Setenv(feature.EnvVar, "true")
			}
			var calls atomic.Int32
			startPreviewCommandServer(t, map[string]any{"agents": map[string]any{
				"long_running": "on", "managed_harness": "on", "voice": "on",
			}}, nil, nil,
				grpc.UnaryInterceptor(func(
					ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
				) (any, error) {
					if strings.HasSuffix(info.FullMethod, tt.method) && calls.Add(1) == 2 {
						if tt.cause == nil {
							return &azdext.GetUserConfigResponse{Found: true, Value: []byte(`{"agents":`)}, nil
						}
						return nil, tt.cause
					}
					return handler(ctx, request)
				}))

			out, err := executeListAlphaCommand(t, "--environment", tt.environment)

			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, featureflags.ManagedHarness.ID)
			if tt.cause != nil {
				require.Equal(t, status.Code(tt.cause), status.Code(err))
			}
			require.Empty(t, out)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestListAlphaCommandRejectsInputsBeforeConnecting(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"json", []string{"--output=json"}, "output"},
		{"table", []string{"--output=table"}, "output"},
		{"none", []string{"--output=none"}, "output"},
		{"empty output", []string{"--output="}, "output"},
		{"positional argument", []string{"agents.voice"}, "unknown command"},
		{"unsupported selector", []string{"--name=agents.voice"}, "unknown flag"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := executeListAlphaCommand(t, tt.args...)
			require.ErrorContains(t, err, tt.want)
			require.NotContains(t, err.Error(), "connecting to azd")
			require.Empty(t, out)
		})
	}
}

func TestListAlphaCommandClientFailure(t *testing.T) {
	t.Setenv("AZD_SERVER", "dns:///%")
	out, err := executeListAlphaCommand(t)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeAzdClientFailed, localErr.Code)
	require.ErrorContains(t, err, "connecting to azd")
	require.Empty(t, out)
}

func TestListAlphaCommandWriterFailure(t *testing.T) {
	clearPreviewProcess(t)
	t.Setenv("AZD_CWD", "")
	startPreviewCommandServer(t, nil, nil, nil)
	root := NewRootCommand()
	root.SetOut(failingStateStoreWriter{})
	root.SetErr(io.Discard)
	root.SetArgs([]string{"list-alpha"})

	err := root.ExecuteContext(t.Context())

	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.ErrorContains(t, err, "writing preview feature list")
}

func TestListAlphaCommandHelpAndMetadata(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	clearPreviewProcess(t)
	root := NewRootCommand()
	command, _, err := root.Find([]string{"list-alpha"})
	require.NoError(t, err)
	require.Equal(t, "list-alpha", command.Name())
	require.False(t, command.Hidden)
	assertOutputFlagOptions(t, command, "default", []string{"default"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	require.NoError(t, command.Help())
	for _, text := range []string{
		"azd ai agent list-alpha", "including disabled features",
		"azd config set alpha.agents.voice on",
		"azd env set AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE true",
		"alpha.all does not enable agent previews",
		"--environment", "--no-prompt", "supported: default",
	} {
		require.Contains(t, out.String(), text)
	}

	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.agents", root)
	for _, entry := range metadata.Commands {
		if len(entry.Name) == 0 || entry.Name[len(entry.Name)-1] != "list-alpha" {
			continue
		}
		require.Equal(t, command.Short, entry.Short)
		for _, flag := range entry.Flags {
			if flag.Name == "output" {
				require.Equal(t, []string{"default"}, flag.ValidValues)
				require.Equal(t, "default", flag.Default)
				return
			}
		}
	}
	t.Fatal("list-alpha output metadata not found")
}

func executeListAlphaCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("AZD_CWD", "")
	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"list-alpha"}, args...))
	err := root.ExecuteContext(t.Context())
	require.Empty(t, errOut.String())
	return out.String(), err
}
