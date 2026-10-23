// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewTestConfig struct {
	azdext.UnimplementedUserConfigServiceServer
	alpha map[string]any
}

func (s *previewTestConfig) Get(
	context.Context, *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	value, err := json.Marshal(s.alpha)
	return &azdext.GetUserConfigResponse{Found: s.alpha != nil, Value: value}, err
}

type previewTestEnvironment struct {
	azdext.UnimplementedEnvironmentServiceServer
	mu       sync.Mutex
	selected string
	values   []*azdext.KeyValue
	err      error
	reads    atomic.Int32
}

func (s *previewTestEnvironment) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selected = request.GetName()
	return &azdext.KeyValueListResponse{KeyValues: s.values}, s.err
}

func startPreviewCommandServer(
	t *testing.T, alpha map[string]any, config *azdext.ProjectConfig, prompts azdext.PromptServiceServer,
) (*azdext.AzdClient, *previewTestEnvironment) {
	t.Helper()
	if config == nil {
		config = &azdext.ProjectConfig{}
	}
	env := &previewTestEnvironment{}
	server := grpc.NewServer(grpc.UnaryInterceptor(func(
		ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		env.reads.Add(1)
		return handler(ctx, request)
	}))
	azdext.RegisterUserConfigServiceServer(server, &previewTestConfig{alpha: alpha})
	azdext.RegisterProjectServiceServer(server, &helpersProjectServer{project: config})
	azdext.RegisterEnvironmentServiceServer(server, env)
	if prompts != nil {
		azdext.RegisterPromptServiceServer(server, prompts)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, env
}

func TestPreviewLocalResponsesWithWebOnlyProject(t *testing.T) {
	clearPreviewProcess(t)
	t.Chdir(t.TempDir())
	startPreviewCommandServer(t, nil, &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{"web": {Name: "web", Host: "appservice"}},
	}, nil)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/responses", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "hello", body["input"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_local","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	_, err = captureStdout(t, func() error {
		_, invokeErr := executeRoot(t, "invoke", "--local", "--protocol", "responses", "--port", port, "hello")
		return invokeErr
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, requests.Load())
	_, err = executeRoot(t, "invoke", "--local", "--protocol", "responses", "--port", port,
		"missing", "hello")
	require.ErrorContains(t, err, "no azure.ai.agent service")
	require.EqualValues(t, 1, requests.Load())
}

func TestPreviewHostedProtocolsRejectBeforeAuthOrWrites(t *testing.T) {
	for _, route := range []string{"image", "code"} {
		for _, protocols := range [][]string{
			{"--protocol", "invocations_ws"},
			{"--protocol", "responses,invocations_ws"},
			{"--protocol", "responses", "--protocol", "invocations_ws"},
		} {
			t.Run(route+"/"+strings.Join(protocols, "_"), func(t *testing.T) {
				clearPreviewProcess(t)
				root := t.TempDir()
				t.Chdir(root)
				startPreviewCommandServer(t, map[string]any{"agents": map[string]any{
					"managed_harness": "on", "long_running": "on",
				}}, nil, nil)
				args := []string{"init", "--no-prompt", "--agent-name", "test-agent"}
				if route == "image" {
					args = append(args, "--image", "registry.example.com/agent:v1")
				} else {
					require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0600))
					args = append(args, "--src", root, "--deploy-mode", "code",
						"--runtime", "python_3_13", "--entry-point", "main.py")
				}
				args = append(args, protocols...)
				_, err := executeRoot(t, args...)
				require.ErrorContains(t, err, featureflags.Voice.ID)
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				if route == "image" {
					require.Empty(t, entries)
				} else {
					require.Len(t, entries, 1)
					require.Equal(t, "main.py", entries[0].Name())
				}
			})
		}
	}
}

func TestPreviewHostedProtocolValidationPrecedesGate(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--image", "invalid", "--agent-name", "agent"}, "registry/image"},
		{[]string{"--image", "registry.example.com/agent:v1"}, "--agent-name"},
		{[]string{"--protocol", "unknown"}, "unknown protocol"},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			clearPreviewProcess(t)
			t.Chdir(t.TempDir())
			startPreviewCommandServer(t, nil, nil, nil)
			args := append([]string{"init", "--no-prompt", "--protocol", "invocations_ws"}, tc.args...)
			_, err := executeRoot(t, args...)
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "is disabled")
		})
	}
}

func TestPreviewHostedProtocolPreRun(t *testing.T) {
	clearPreviewProcess(t)
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			t.Setenv(featureflags.Voice.EnvVar, strconv.FormatBool(enabled))
			_, env := startPreviewCommandServer(t, nil, nil, nil)
			for _, protocols := range [][]string{
				nil, {"--protocol", "responses"}, {"--protocol", "invocations"},
				{"--protocol", "activity"}, {"--protocol", "invocations_ws"},
				{"--protocol", "responses,invocations_ws"},
				{"--protocol", "responses", "--protocol", "invocations_ws"},
			} {
				root := NewRootCommand()
				cmd, _, err := root.Find([]string{"init"})
				require.NoError(t, err)
				cmd.SetContext(t.Context())
				require.NoError(t, cmd.ParseFlags(protocols))
				before := env.reads.Load()
				err = enforcePreviewFeatures(cmd, nil)
				if !strings.Contains(strings.Join(protocols, ","), "invocations_ws") {
					require.NoError(t, err)
					require.Empty(t, requiredPreviewFeatures(cmd))
					require.Equal(t, before, env.reads.Load())
				} else if enabled {
					require.NoError(t, err)
					require.Equal(t, []featureflags.Feature{featureflags.Voice}, requiredPreviewFeatures(cmd))
				} else {
					require.ErrorContains(t, err, featureflags.Voice.ID)
				}
				require.Equal(t, before, env.reads.Load())
			}
		})
	}
}

func TestPreviewLocalServicePreservesProjectErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server *helpersProjectServer
		want   string
	}{
		{"permission denied", &helpersProjectServer{err: status.Error(codes.PermissionDenied, "project denied")},
			"project denied"},
		{"nil project", &helpersProjectServer{}, "missing project response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPreviewProcess(t)
			server := grpc.NewServer()
			azdext.RegisterProjectServiceServer(server, tc.server)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			t.Setenv("AZD_SERVER", listener.Addr().String())
			t.Setenv("AZD_ACCESS_TOKEN", "test-token")
			action := &InvokeAction{flags: &invokeFlags{}}
			require.ErrorContains(t, action.selectLocalService(t.Context()), tc.want)
		})
	}
}

type protocolSelectionServer struct {
	azdext.UnimplementedPromptServiceServer
	selectProtocols func(*azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error)
}

func (s *protocolSelectionServer) MultiSelect(
	_ context.Context, request *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	return s.selectProtocols(request)
}

func TestPreviewHostedProtocolPickerBeforeMutation(t *testing.T) {
	for _, route := range []string{"image", "code"} {
		for _, enabled := range []bool{false, true} {
			t.Run(route+"/"+strconv.FormatBool(enabled), func(t *testing.T) {
				clearPreviewProcess(t)
				root := t.TempDir()
				t.Chdir(root)
				var calls atomic.Int32
				prompts := &protocolSelectionServer{selectProtocols: func(req *azdext.MultiSelectRequest) (
					*azdext.MultiSelectResponse, error,
				) {
					calls.Add(1)
					var names []string
					for _, choice := range req.Options.Choices {
						names = append(names, choice.Value)
						require.Equal(t, choice.Value == "responses", choice.Selected)
					}
					if enabled {
						require.Equal(t, []string{"responses", "invocations", "invocations_ws", "activity"}, names)
					} else {
						require.Equal(t, []string{"responses", "invocations", "activity"}, names)
					}
					return nil, status.Error(codes.Canceled, "stop before project setup")
				}}
				t.Setenv(featureflags.Voice.EnvVar, strconv.FormatBool(enabled))
				client, _ := startPreviewCommandServer(t, nil, nil, prompts)
				flags := &initFlags{agentName: "agent"}
				if route == "image" {
					flags.image = "registry.example.com/agent:v1"
				} else {
					require.NoError(t, os.WriteFile("main.py", []byte("print('hello')\n"), 0600))
					flags.src = root
				}
				action := &InitFromCodeAction{azdClient: client, flags: flags, sourceValidated: true}
				err := action.Run(t.Context())
				require.ErrorContains(t, err, "protocol selection was cancelled")
				require.EqualValues(t, 1, calls.Load())
				require.Nil(t, action.projectConfig)
				require.Nil(t, action.environment)
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				require.Len(t, entries, map[string]int{"image": 0, "code": 1}[route])
			})
		}
	}
}

func TestPreviewHostedProtocolSelection(t *testing.T) {
	clearPreviewProcess(t)
	for _, explicit := range [][]string{nil, {"responses"}, {"invocations"}, {"activity"}, {"responses", "invocations"}} {
		records, err := selectHostedProtocols(t.Context(), nil, true, explicit)
		require.NoError(t, err, "ordinary explicit/default protocols need no feature reads")
		require.NotEmpty(t, records)
	}
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			t.Setenv(featureflags.Voice.EnvVar, strconv.FormatBool(enabled))
			prompts := &protocolSelectionServer{selectProtocols: func(*azdext.MultiSelectRequest) (
				*azdext.MultiSelectResponse, error,
			) {
				return &azdext.MultiSelectResponse{Values: []*azdext.MultiSelectChoice{
					{Value: "invocations_ws", Selected: true},
				}}, nil
			}}
			client, env := startPreviewCommandServer(t, nil, nil, prompts)
			records, err := selectHostedProtocols(t.Context(), client, false, nil)
			if enabled {
				require.NoError(t, err)
				require.Equal(t, []agent_yaml.ProtocolVersionRecord{
					{Protocol: "invocations_ws", Version: "2.0.0"},
				}, records)
			} else {
				require.ErrorContains(t, err, "unexpected protocol")
			}
			before := env.reads.Load()
			_, err = selectHostedProtocols(t.Context(), client, true, []string{"responses", "invocations_ws"})
			if enabled {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, featureflags.Voice.ID)
			}
			require.Equal(t, before, env.reads.Load())
			env.mu.Lock()
			require.Empty(t, env.selected)
			env.err = status.Error(codes.PermissionDenied, "selection source failure")
			env.mu.Unlock()
			_, err = selectHostedProtocols(t.Context(), client, false, nil)
			if enabled {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "unexpected protocol")
			}
			require.Equal(t, before+1, env.reads.Load(), "only the protocol prompt calls the host")
		})
	}
}

func clearPreviewProcess(t *testing.T) {
	t.Helper()
	setPreviewProcess(t, "")
}

// setPreviewProcess also clears host selection for offline fixtures.
func setPreviewProcess(t *testing.T, value string) {
	t.Helper()
	for _, feature := range featureflags.All() {
		t.Setenv(feature.EnvVar, value)
	}
	t.Setenv("AZD_ENVIRONMENT", "")
	t.Setenv("AZD_SERVER", "")
}

// executeRoot runs the extension root so the central pre-run applies.
func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func gatedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, serviceGateKey{}, true)
}

func TestPersistedConfigCannotUnlockPreviewGates(t *testing.T) {
	clearPreviewProcess(t)
	for _, alpha := range []map[string]any{
		{"all": "on"}, {"all": "ON"}, {"all": "On"}, {"all": "oN"},
		{"agents": map[string]any{"managed_harness": "on", "voice": "on", "long_running": "on"}},
	} {
		name, err := json.Marshal(alpha)
		require.NoError(t, err)
		t.Run(string(name), func(t *testing.T) {
			t.Chdir(t.TempDir())
			_, env := startPreviewCommandServer(t, alpha, nil, nil)
			for _, tc := range []struct {
				args    []string
				feature featureflags.Feature
			}{
				{[]string{"init", "--no-prompt", "--kind", "prompt", "--harness", "microsoft_copilot_preview"},
					featureflags.ManagedHarness},
				{[]string{"init", "--no-prompt", "--kind", "prompt-voice", "--agent-name", "voice-agent"},
					featureflags.Voice},
				{[]string{"invoke", "--long-running", "--no-wait", "hello"}, featureflags.LongRunning},
			} {
				_, err := executeRoot(t, tc.args...)
				require.ErrorContains(t, err, tc.feature.ID)
			}
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Zero(t, env.reads.Load())
		})
	}
}

func TestIndependentPreviewGates(t *testing.T) {
	clearPreviewProcess(t)
	features := []featureflags.Feature{
		featureflags.ManagedHarness, featureflags.Voice, featureflags.LongRunning,
	}
	for _, enabled := range features {
		t.Run(enabled.ID, func(t *testing.T) {
			t.Setenv(enabled.EnvVar, "true")
			for _, candidate := range features {
				err := featureflags.Require(candidate)
				if candidate == enabled {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, candidate.ID)
				}
			}
		})
	}
}

func TestPreviewGuidanceDoesNotExposeServiceOptIn(t *testing.T) {
	clearPreviewProcess(t)
	for _, feature := range []featureflags.Feature{
		featureflags.ManagedHarness, featureflags.Voice, featureflags.LongRunning,
	} {
		t.Run(feature.ID, func(t *testing.T) {
			err := featureflags.Require(feature)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			guidance := localErr.Message + " " + localErr.Suggestion
			for _, hidden := range []string{"azure.yaml", "services[].env", "service env"} {
				require.NotContains(t, strings.ToLower(guidance), hidden)
			}
			require.NotContains(t, guidance, "azd config set")
			require.NotContains(t, guidance, "azd env set")
			require.Contains(t, localErr.Suggestion, feature.EnvVar+"=true in your shell or CI")
			t.Setenv(feature.EnvVar, "true")
			require.NoError(t, featureflags.Require(feature))
		})
	}
}

func TestPreviewInitRejectsBeforeAuthOrProjectWrites(t *testing.T) {
	clearPreviewProcess(t)
	for _, tc := range []struct {
		args    []string
		feature featureflags.Feature
	}{
		{[]string{"--kind", "prompt", "--harness", "microsoft_copilot_preview"}, featureflags.ManagedHarness},
		{[]string{"--kind", "prompt", "--harness", "github_copilot_preview"}, featureflags.ManagedHarness},
		{[]string{"--kind", "prompt-voice", "--agent-name", "voice-agent"}, featureflags.Voice},
		{[]string{"--kind", " Prompt-Voice ", "--agent-name", "voice-agent"}, featureflags.Voice},
		{[]string{"--voice", "en-US-Ava:DragonHDLatestNeural", "--kind", "prompt-voice",
			"--agent-name", "voice-agent"}, featureflags.Voice},
	} {
		t.Run(tc.feature.ID+"/"+tc.args[len(tc.args)-1], func(t *testing.T) {
			t.Chdir(t.TempDir())
			_, env := startPreviewCommandServer(t, nil, nil, nil)
			args := append([]string{"init", "-e", "selected-env", "--no-prompt"}, tc.args...)
			_, err := executeRoot(t, args...)
			require.ErrorContains(t, err, tc.feature.ID)
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Zero(t, env.reads.Load())
		})
	}
}

func TestPreviewVoiceDiscovery(t *testing.T) {
	clearPreviewProcess(t)
	t.Chdir(t.TempDir())
	prompts := &helpersPromptServer{selectIndex: 0}
	client, _ := startPreviewCommandServer(t, nil, nil, prompts)
	mode, err := promptInitMode(t.Context(), client, false)
	require.NoError(t, err)
	require.Equal(t, initModeTemplate, mode)
	require.Len(t, prompts.lastSelect.Options.Choices, 1)
	require.NotContains(t, prompts.lastSelect.Options.Choices[0].Label, "voice")
	t.Setenv(featureflags.Voice.EnvVar, "true")
	_, err = promptInitMode(t.Context(), client, false)
	require.NoError(t, err)
	require.Len(t, prompts.lastSelect.Options.Choices, 2)
}

func TestPreviewStagedManifestUsesProcessSwitch(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	content := []byte("name: preview-test\nservices:\n  voice:\n    host: azure.ai.agent\n    kind: prompt-voice\n")
	require.ErrorContains(t, requireManifestPreviewFeatures(content, root), "agents.voice")
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0600))
	require.ErrorContains(t, requireStagedPreviewFeatures(root), "agents.voice")
	require.ErrorContains(t, requireManifestPreviewFeatures(
		append(content, []byte("  web:\n    host: containerapp\n    env:\n      "+
			featureflags.Voice.EnvVar+": 'true'\n")...), root), "agents.voice")
	require.ErrorContains(t, requireManifestPreviewFeatures(
		append(content, []byte("    env:\n      "+featureflags.Voice.EnvVar+": true\n")...), root), "agents.voice")
	require.ErrorContains(t, project.RequireDefinitionFeatures(map[string]any{
		"kind": "prompt-voice", "env": map[string]any{featureflags.Voice.EnvVar: true},
	}), "agents.voice")
	t.Setenv(featureflags.Voice.EnvVar, "true")
	require.NoError(t, requireManifestPreviewFeatures(content, root))
	require.NoError(t, requireStagedPreviewFeatures(root))
	require.NoError(t, project.RequireDefinitionFeatures(map[string]any{"kind": "prompt-voice"}))
	got, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestPreviewDefinitionGateNeedsNoHost(t *testing.T) {
	clearPreviewProcess(t)
	t.Setenv("AZD_SERVER", "127.0.0.1:1")
	definition := map[string]any{
		"kind": "prompt-voice", "env": map[string]any{featureflags.Voice.EnvVar: true},
	}
	require.ErrorContains(t, project.RequireDefinitionFeatures(definition), "agents.voice")
	t.Setenv(featureflags.Voice.EnvVar, "true")
	require.NoError(t, project.RequireDefinitionFeatures(definition))
}

func TestPreviewInvokeLocalBypassesAndReadAccess(t *testing.T) {
	clearPreviewProcess(t)
	for _, definition := range []map[string]any{
		{"kind": "prompt", "harness": map[string]any{"type": "microsoft_copilot_preview"}},
		{"kind": "prompt-voice"},
		{"kind": "hosted", "protocols": []any{map[string]any{"protocol": "invocations_ws"}}},
	} {
		props, err := structpb.NewStruct(definition)
		require.NoError(t, err)
		config := &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"agent": {Name: "agent", Host: AiAgentHost, AdditionalProperties: props},
		}}
		before := proto.CloneOf(config)
		client, _ := startPreviewCommandServer(t, nil, config, nil)
		ctx := gatedContext(t.Context())
		for _, protocol := range []string{"responses", "invocations", "a2a"} {
			action := &InvokeAction{flags: &invokeFlags{
				local: true, protocol: protocol, name: "agent", message: "hello",
			}, noPrompt: true}
			require.ErrorContains(t, action.Run(ctx), "disabled")
			action.flags.local = false
			require.ErrorContains(t, action.Run(ctx), "disabled")
		}

		_, err = resolveServiceRunContext(ctx, client, "agent", true)
		require.ErrorContains(t, err, "disabled")
		_, err = resolveAgentServiceFromProject(ctx, client, "agent", true, withDeployedAgentNameLookup())
		require.ErrorContains(t, err, "disabled", "gate errors must not fall back to deployed-name lookup")
		_, err = resolveAgentServiceFromProject(t.Context(), client, "agent", true)
		require.NoError(t, err, "read and cleanup callers must not require preview opt-in")
		_, _, err = resolveAgentService(t.Context(), client, "agent", true)
		require.NoError(t, err, "commands without the service gate select freely")
		require.True(t, proto.Equal(before, config))
	}
}

func TestPreviewOptimizeDeployRejectsLocalHarnessBeforeAuth(t *testing.T) {
	clearPreviewProcess(t)
	props, err := structpb.NewStruct(map[string]any{
		"kind": "prompt", "harness": map[string]any{"type": "microsoft_copilot_preview"},
	})
	require.NoError(t, err)
	_, env := startPreviewCommandServer(t, nil, &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"agent": {Name: "agent", Host: AiAgentHost, AdditionalProperties: props},
		},
	}, nil)
	_, err = executeRoot(t, "optimize", "deploy", "-e", "selected-env",
		"--agent", "agent", "--candidate", "candidate-test")
	require.ErrorContains(t, err, "agents.managed_harness")
	env.mu.Lock()
	require.Empty(t, env.selected)
	env.mu.Unlock()
}

func TestPreviewMutationCommandsRejectDisabledServices(t *testing.T) {
	for _, tc := range []struct {
		name       string
		definition map[string]any
		args       []string
		feature    string
	}{
		{"endpoint update", map[string]any{
			"kind": "hosted", "protocols": []any{map[string]any{"protocol": "invocations_ws"}},
		}, []string{"endpoint", "update", "agent"}, "agents.voice"},
		{"optimize apply", map[string]any{
			"kind": "prompt", "harness": map[string]any{"type": "microsoft_copilot_preview"},
		}, []string{"optimize", "apply", "--agent", "agent", "--candidate", "candidate-test"}, "agents.managed_harness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPreviewProcess(t)
			root := t.TempDir()
			t.Chdir(root)
			props, err := structpb.NewStruct(tc.definition)
			require.NoError(t, err)
			config := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{
				"agent": {Name: "agent", Host: AiAgentHost, AdditionalProperties: props},
			}}
			before := proto.CloneOf(config)
			startPreviewCommandServer(t, nil, config, nil)
			_, err = executeRoot(t, append([]string{"-e", "selected-env"}, tc.args...)...)
			require.ErrorContains(t, err, tc.feature)
			require.True(t, proto.Equal(before, config))
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Empty(t, entries, "disabled previews must not write project files")
		})
	}
}

func TestPreviewExplicitLocalTemplateBeforeAuth(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	t.Chdir(root)
	content := []byte("name: preview-test\nservices:\n  voice:\n    host: azure.ai.agent\n" +
		"    kind: prompt-voice\n    name: voice-test\n    modelType: managed\n" +
		"    model:\n      id: gpt-realtime\n")
	require.NoError(t, os.WriteFile("azure.yaml", content, 0600))
	startPreviewCommandServer(t, nil, nil, nil)
	command := newInitCommand(&azdext.ExtensionContext{NoPrompt: true})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--template", filepath.Join(root, "azure.yaml")})
	require.ErrorContains(t, command.Execute(), "agents.voice")
	actual, err := os.ReadFile("azure.yaml")
	require.NoError(t, err)
	require.Equal(t, content, actual)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestPreviewLongRunningDirectEndpoint(t *testing.T) {
	clearPreviewProcess(t)
	_, env := startPreviewCommandServer(t, nil, nil, nil)
	endpoint := "https://acct.services.ai.azure.com/api/projects/proj/agents/agent/" +
		"endpoint/protocols/openai/responses?api-version=v1"
	for _, args := range [][]string{
		{"--long-running"},
		{"--long-running", "--no-wait"},
	} {
		all := append([]string{"invoke", "-e", "selected-env", "--agent-endpoint", endpoint}, args...)
		_, err := executeRoot(t, append(all, "hello")...)
		require.ErrorContains(t, err, "agents.long_running")
	}
	require.Zero(t, env.reads.Load())
	action := &InvokeAction{
		flags:    &invokeFlags{protocol: "responses", message: "hello"},
		endpoint: &parsedAgentEndpoint{}, credential: invokeUsageFailingCredential{},
		resolvedRemoteContext: &remoteContext{name: "ordinary-http-endpoint"},
	}
	require.ErrorContains(t, action.Run(t.Context()), "failed to get auth token")
}

func TestPreviewAgentAddRejectsBeforeWrite(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	manifest := []byte("name: preview-test\nservices:\n  ai-project:\n    host: azure.ai.project\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), manifest, 0600))
	definition := agentServiceAddDefinition + "harness:\n  type: microsoft_copilot_preview\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte(definition), 0600))
	startPreviewCommandServer(t, nil, nil, nil)
	client := &recordingAgentServiceAddClient{projectPath: root}
	action := &agentServiceAddAction{
		projectClient: client, flags: agentServiceAddActionFlags{serviceName: "agent", file: "agent.yaml"},
		outputFormat: "default", out: &bytes.Buffer{}, errOut: &bytes.Buffer{},
	}
	require.ErrorContains(t, action.Run(t.Context()), "agents.managed_harness")
	require.Empty(t, client.addRequests)
	require.Empty(t, client.setRequests)
	actual, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, manifest, actual)
}
