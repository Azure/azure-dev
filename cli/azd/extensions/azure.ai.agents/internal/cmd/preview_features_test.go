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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaiagent/internal/featureflags"
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
	t *testing.T, alpha map[string]any, config *azdext.ProjectConfig, prompts *helpersPromptServer,
) (*azdext.AzdClient, *previewTestEnvironment) {
	t.Helper()
	if config == nil {
		config = &azdext.ProjectConfig{}
	}
	server := grpc.NewServer()
	env := &previewTestEnvironment{}
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

func clearPreviewProcess(t *testing.T) {
	t.Helper()
	setPreviewProcess(t, "")
}

// setPreviewProcess sets every registered feature's process value and
// clears host selection so help and enforcement read only that value.
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

func TestGlobalConfigCannotUnlockPreviewGates(t *testing.T) {
	clearPreviewProcess(t)
	for _, value := range []string{"on", "ON", "On", "oN"} {
		t.Run(value, func(t *testing.T) {
			t.Chdir(t.TempDir())
			startPreviewCommandServer(t, map[string]any{"all": value}, nil, nil)
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
			client, env := startPreviewCommandServer(t, map[string]any{
				"all":    "on",
				"agents": map[string]any{enabled.ID[len("agents."):]: "on"},
			}, nil, nil)
			ctx := featureflags.WithEnvironment(t.Context(), "selected-env")
			for _, candidate := range features {
				err := featureflags.Require(ctx, client, candidate)
				if candidate == enabled {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, candidate.ID)
				}
			}
			env.mu.Lock()
			require.Equal(t, "selected-env", env.selected)
			env.mu.Unlock()
		})
	}
	client, _ := startPreviewCommandServer(t, map[string]any{
		"agents": map[string]any{"hosted": "on"},
	}, nil, nil)
	for _, feature := range features {
		require.ErrorContains(t, featureflags.Require(t.Context(), client, feature), feature.ID)
		t.Setenv(feature.EnvVar, "true")
		require.NoError(t, featureflags.Require(t.Context(), client, feature))
		t.Setenv(feature.EnvVar, "")
	}
}

func TestPreviewGuidanceDoesNotExposeServiceOptIn(t *testing.T) {
	clearPreviewProcess(t)
	for _, feature := range []featureflags.Feature{
		featureflags.ManagedHarness, featureflags.Voice, featureflags.LongRunning,
	} {
		t.Run(feature.ID, func(t *testing.T) {
			client, _ := startPreviewCommandServer(t, nil, nil, nil)
			err := featureflags.Require(t.Context(), client, feature)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			guidance := localErr.Message + " " + localErr.Suggestion
			for _, hidden := range []string{"azure.yaml", "services[].env", "service env"} {
				require.NotContains(t, strings.ToLower(guidance), hidden)
			}
			require.Contains(t, localErr.Suggestion, "azd config set alpha."+feature.ID+" on")
			require.Contains(t, localErr.Suggestion, "azd env set "+feature.EnvVar+" true")
			require.Contains(t, localErr.Suggestion, feature.EnvVar+"=true in the process environment")

			client, _ = startPreviewCommandServer(t, nil, &azdext.ProjectConfig{
				Services: map[string]*azdext.ServiceConfig{
					"web": {Host: "appservice", Environment: map[string]string{feature.EnvVar: "true"}},
				},
			}, nil)
			require.NoError(t, featureflags.Require(t.Context(), client, feature))
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
		{[]string{"--voice", "en-US-Ava:DragonHDLatestNeural"}, featureflags.Voice},
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
			env.mu.Lock()
			require.Equal(t, "selected-env", env.selected)
			env.mu.Unlock()
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

func TestPreviewStagedManifestAndServiceOptIn(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	client, _ := startPreviewCommandServer(t, nil, nil, nil)
	content := []byte("name: preview-test\nservices:\n  voice:\n    host: azure.ai.agent\n    kind: prompt-voice\n")
	require.ErrorContains(t, requireManifestPreviewFeatures(t.Context(), client, content, root), "agents.voice")
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0600))
	require.ErrorContains(t, requireStagedPreviewFeatures(t.Context(), client, root), "agents.voice")
	require.NoError(t, requireManifestPreviewFeatures(t.Context(), client,
		append(content, []byte("  web:\n    host: containerapp\n    env:\n      "+
			featureflags.Voice.EnvVar+": 'true'\n")...), root))
	require.NoError(t, requireManifestPreviewFeatures(t.Context(), client,
		append(content, []byte("    env:\n      "+featureflags.Voice.EnvVar+": true\n")...), root))
	require.NoError(t, requireDefinitionPreviewFeatures(t.Context(), map[string]any{
		"kind": "prompt-voice", "env": map[string]any{featureflags.Voice.EnvVar: true},
	}))
	require.ErrorContains(t, requireDefinitionPreviewFeatures(t.Context(), map[string]any{
		"kind": "prompt-voice",
	}), "agents.voice", "prospective opt-ins must not leak into later checks")
	got, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestPreviewProspectiveOptInPreservesReadErrors(t *testing.T) {
	clearPreviewProcess(t)
	client, env := startPreviewCommandServer(t, nil, nil, nil)
	env.mu.Lock()
	env.err = status.Error(codes.PermissionDenied, "cannot read selected environment")
	env.mu.Unlock()
	ctx := featureflags.WithEnvironment(t.Context(), "selected-env")
	definition := map[string]any{
		"kind": "prompt-voice", "env": map[string]any{featureflags.Voice.EnvVar: true},
	}
	require.ErrorContains(t, project.RequireDefinitionFeatures(ctx, client, definition),
		"cannot read selected environment")
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
	require.Equal(t, "selected-env", env.selected)
	env.mu.Unlock()
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
		{"--no-wait"},
	} {
		all := append([]string{"invoke", "-e", "selected-env", "--agent-endpoint", endpoint}, args...)
		_, err := executeRoot(t, append(all, "hello")...)
		require.ErrorContains(t, err, "agents.long_running")
	}
	env.mu.Lock()
	require.Equal(t, "selected-env", env.selected)
	env.mu.Unlock()
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
