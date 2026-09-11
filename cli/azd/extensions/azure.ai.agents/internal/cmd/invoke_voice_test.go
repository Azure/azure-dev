// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestVoiceInvokeCommandPortalGuidance(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fields map[string]any
	}{
		{"conversation-engine", map[string]any{
			"kind":               "voice",
			"conversationEngine": map[string]any{"type": "hosted_agent", "name": "voice-target"},
		}},
		{"conversation-engine-kind-alias", map[string]any{
			"kind":               "prompt-voice",
			"conversationEngine": map[string]any{"type": "hosted_agent", "name": "voice-target"},
		}},
		// Unsupported old authoring still identifies a voice service for invoke guidance;
		// these cases do not assert that this shape can be initialized or deployed.
		{"unsupported-old-hosted-wrapper", map[string]any{
			"kind": "voice", "modelType": "hosted_agent",
			"targetAgent": map[string]any{"service": "voice-target"},
		}},
		{"unsupported-old-hosted-wrapper-kind-alias", map[string]any{
			"kind": "prompt-voice", "modelType": "hosted_agent",
			"targetAgent": map[string]any{"service": "voice-target"},
		}},
		{"managed-prompt-voice", map[string]any{
			"kind": "voice", "modelType": "managed", "model": map[string]any{"id": "gpt-realtime"},
		}},
		{"byom-prompt-voice", map[string]any{
			"kind": "prompt-voice", "modelType": "self_deployed", "model": map[string]any{"id": "my-realtime"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			props, err := structpb.NewStruct(tt.fields)
			require.NoError(t, err)
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"voice-service": {Name: "voice-service", Host: AiAgentHost, AdditionalProperties: props},
				},
			}}
			server := grpc.NewServer()
			azdext.RegisterProjectServiceServer(server, project)
			azdext.RegisterPromptServiceServer(server, &helpersPromptServer{})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			for _, args := range [][]string{
				{"hello"},
				{"voice-service", "hello"},
				{"--local", "hello"},
				{"--protocol", "responses", "hello"},
				{"--protocol", "responses", "voice-service", "hello"},
				{"--protocol", "invocations", "voice-service", "hello"},
				{"--protocol", "a2a", "voice-service", "hello"},
			} {
				command := newInvokeCommand(nil)
				var buf bytes.Buffer
				command.SetOut(&buf)
				command.SetErr(&buf)
				command.SetArgs(args)
				err := command.Execute()
				require.ErrorIs(t, err, errVoiceInvocationUnsupported, "args: %v", args)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, localErr.Suggestion, "https://ai.azure.com")
			}
			command := newInvokeCommand(nil)
			var buf bytes.Buffer
			command.SetOut(&buf)
			command.SetErr(&buf)
			command.SetArgs([]string{})
			require.ErrorContains(t, command.Execute(), "a message argument or --input-file is required")
		})
	}
}

func TestVoiceInvocationGuidance(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"voice", "prompt-voice"} {
		for _, modelType := range []string{"managed", "self_deployed", "hosted_agent"} {
			t.Run(kind+"/"+modelType, func(t *testing.T) {
				t.Parallel()
				props, err := structpb.NewStruct(map[string]any{"kind": kind, "modelType": modelType})
				require.NoError(t, err)
				project := &helpersProjectServer{project: &azdext.ProjectConfig{
					Path: t.TempDir(),
					Services: map[string]*azdext.ServiceConfig{
						"voice-service": {Name: "voice-service", Host: AiAgentHost, AdditionalProperties: props},
					},
				}}
				client := newHelpersTestAzdClient(t, project, &helpersPromptServer{})
				// No environment/auth service is registered: both paths must stop before reaching it.
				_, _, err = resolveAgentProtocol(t.Context(), client, "", true)
				require.ErrorIs(t, err, errVoiceInvocationUnsupported)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Contains(t, localErr.Suggestion, "Microsoft Foundry portal")
				require.Contains(t, localErr.Suggestion, "https://ai.azure.com")
				_, err = resolveAgentServiceFromProject(
					t.Context(), client, "voice-service", true,
					withBrownfieldInlineAgentName(), withVoiceInvocationGuidance(),
				)
				require.ErrorIs(t, err, errVoiceInvocationUnsupported)
				require.ErrorIs(t, remoteAgentServiceResolutionError(err, true), errVoiceInvocationUnsupported)
				require.ErrorIs(t, remoteAgentServiceResolutionError(err, false), errVoiceInvocationUnsupported)

				// Other shared callers (show/delete/etc.) do not opt in to the invoke guard.
				info, err := resolveAgentServiceFromProject(t.Context(), client, "voice-service", true)
				require.NoError(t, err)
				require.Equal(t, "voice-service", info.ServiceName)
			})
		}
	}
}

func TestVoiceInvocationOverridePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, inline, override string
		wantVoice              bool
	}{
		{"hosted override wins", "voice", "kind: hosted\n", false},
		{"voice override wins", "hosted", "kind: voice\n", true},
		{"alias override wins", "hosted", "kind: prompt-voice\n", true},
		{"malformed override is not guessed", "voice", "kind: [\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "override.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.override), 0600))
			t.Setenv("AGENT_DEFINITION_PATH", path)
			props, err := structpb.NewStruct(map[string]any{"kind": tt.inline})
			require.NoError(t, err)
			err = voiceInvocationError(&azdext.ServiceConfig{AdditionalProperties: props}, root)
			if tt.wantVoice {
				require.ErrorIs(t, err, errVoiceInvocationUnsupported)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVoiceInvocationGuidanceDoesNotClassifyManagedPrompt(t *testing.T) {
	t.Parallel()
	for _, withHarness := range []bool{false, true} {
		fields := map[string]any{
			"kind": "prompt", "name": "managed-prompt", "model": "my-deployment", "instructions": "Be helpful.",
		}
		if withHarness {
			fields["harness"] = map[string]any{"type": "github_copilot"}
		}
		props, err := structpb.NewStruct(fields)
		require.NoError(t, err)
		require.NoError(t, voiceInvocationError(&azdext.ServiceConfig{
			Name: "managed-prompt", Host: AiAgentHost, AdditionalProperties: props,
		}, t.TempDir()))
	}
}

func TestVoiceInvocationDetectionCompatibility(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"hosted", "workflow", "prompt", "", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			props, err := structpb.NewStruct(map[string]any{"kind": kind})
			require.NoError(t, err)
			require.NoError(t, voiceInvocationError(&azdext.ServiceConfig{AdditionalProperties: props}, t.TempDir()))
		})
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "voice.yaml"), []byte("kind: voice\n"), 0600))
	ref, err := structpb.NewStruct(map[string]any{"$ref": "voice.yaml"})
	require.NoError(t, err)
	require.ErrorIs(t, voiceInvocationError(&azdext.ServiceConfig{AdditionalProperties: ref}, root),
		errVoiceInvocationUnsupported)
	legacy, err := structpb.NewStruct(map[string]any{"kind": "prompt-voice"})
	require.NoError(t, err)
	require.ErrorIs(t, voiceInvocationError(&azdext.ServiceConfig{Config: legacy}, root), errVoiceInvocationUnsupported)
	missing, err := structpb.NewStruct(map[string]any{"$ref": "missing.yaml"})
	require.NoError(t, err)
	require.NoError(t, voiceInvocationError(&azdext.ServiceConfig{AdditionalProperties: missing}, root))
	require.NoError(t, voiceInvocationError(nil, root))
}

func TestVoiceInvocationGuidanceByDeployedName(t *testing.T) {
	t.Parallel()
	props, err := structpb.NewStruct(map[string]any{
		"kind":               "voice",
		"conversationEngine": map[string]any{"type": "hosted_agent", "name": "target"},
	})
	require.NoError(t, err)
	project := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"voice-service": {Name: "voice-service", Host: AiAgentHost, AdditionalProperties: props},
		},
	}}
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values:  map[string]map[string]string{"test": {"AGENT_VOICE_SERVICE_NAME": "deployed-voice"}},
	}
	client := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
	_, err = resolveAgentServiceFromProject(
		t.Context(), client, "deployed-voice", true,
		withDeployedAgentNameLookup(), withDeployedProtocolEndpoints(), withVoiceInvocationGuidance(),
	)
	require.ErrorIs(t, err, errVoiceInvocationUnsupported)
	require.ErrorIs(t, remoteAgentServiceResolutionError(err, true), errVoiceInvocationUnsupported)
}

func TestVoiceInvocationGuidancePreservesHostedProtocols(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"responses", "invocations", "a2a", "invocations_ws"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			props, err := structpb.NewStruct(map[string]any{
				"kind": "hosted", "name": "voice-target",
				"protocols": []any{map[string]any{"protocol": protocol, "version": "1.0"}},
				"metadata":  map[string]any{"voiceLiveCompatible": "true", "bridgeProtocolVersion": "1.0"},
			})
			require.NoError(t, err)
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"voice-target": {Name: "voice-target", Host: AiAgentHost, AdditionalProperties: props},
				},
			}}
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "test"},
				values:  map[string]map[string]string{"test": {"AGENT_VOICE_TARGET_NAME": "deployed-target"}},
			}
			client := newHelpersTestAzdClient(t, project, &helpersPromptServer{}, env)
			got, _, err := resolveAgentProtocol(t.Context(), client, "", true)
			if protocol == "invocations_ws" {
				require.ErrorContains(t, err, "non-invocable protocols")
				require.NotErrorIs(t, err, errVoiceInvocationUnsupported)
			} else {
				require.NoError(t, err)
				require.Equal(t, protocol, string(got))
			}
			before, err := resolveAgentServiceFromProject(t.Context(), client, "voice-target", true)
			require.NoError(t, err)
			after, err := resolveAgentServiceFromProject(
				t.Context(), client, "voice-target", true, withVoiceInvocationGuidance(),
			)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestVoiceInvocationGuidanceSelection(t *testing.T) {
	t.Parallel()
	voiceProps, err := structpb.NewStruct(map[string]any{
		"kind":               "voice",
		"conversationEngine": map[string]any{"type": "hosted_agent", "name": "a-target"},
	})
	require.NoError(t, err)
	hostedProps, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "target",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0"}},
	})
	require.NoError(t, err)
	for _, explicit := range []bool{false, true} {
		for _, index := range []int32{0, 1} {
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"a-target": {Name: "a-target", Host: AiAgentHost, AdditionalProperties: hostedProps},
					"b-voice":  {Name: "b-voice", Host: AiAgentHost, AdditionalProperties: voiceProps},
				},
			}}
			prompts := &helpersPromptServer{selectIndex: index}
			client := newHelpersTestAzdClient(t, project, prompts)
			if explicit {
				_, err = resolveAgentServiceFromProject(t.Context(), client, "", false, withVoiceInvocationGuidance())
			} else {
				_, _, err = resolveAgentProtocol(t.Context(), client, "", false)
			}
			if index == 1 {
				require.ErrorIs(t, err, errVoiceInvocationUnsupported)
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, prompts.selectCalls.Load())
			_, _, err = resolveAgentProtocol(t.Context(), client, "", true)
			require.ErrorContains(t, err, "multiple azure.ai.agent services")
			require.EqualValues(t, 1, prompts.selectCalls.Load())
		}
	}
}
