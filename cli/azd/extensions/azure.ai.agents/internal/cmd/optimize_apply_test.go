// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// ---- newOptimizeApplyCommand — command shape ----

func TestNewOptimizeApplyCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "apply", cmd.Use)
}

func TestNewOptimizeApplyCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})

	require.NotNil(t, cmd.Flags().Lookup("candidate"))
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("endpoint"))
	require.NotNil(t, cmd.Flags().Lookup("project-endpoint"))
}

func TestNewOptimizeApplyCommand_CandidateIsRequired(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "candidate")
}

func TestPersistInlineAgentEnvironmentMigratesLegacyTemplates(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:   "basic-agent",
		Host:   AiAgentHost,
		Config: props,
	}

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.added)
	require.Equal(
		t,
		[]string{"config.environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"LEGACY_KEY":                "${LEGACY_KEY}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

// TestPersistInlineAgentEnvironmentPreservesTopLevelEnv verifies a
// modern agent's top-level env templates survive the OPTIMIZATION_*
// update: they are read raw and rewritten via the env section, not
// snapshotted to expanded literals through AddService.
func TestPersistInlineAgentEnvironmentPreservesTopLevelEnv(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
		// Core forwards expanded values; the raw templates live on disk.
		Environment: map[string]string{
			"LOG_LEVEL":      "debug",
			"MODEL_ENDPOINT": "https://resolved.example",
		},
	}

	server := &recordingProjectServer{
		rawEnv: map[string]map[string]any{
			"basic-agent": {
				"LOG_LEVEL":      "${AZURE_LOG_LEVEL}",
				"MODEL_ENDPOINT": "$${{project.endpoint}}",
			},
		},
	}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.added)
	require.Equal(
		t,
		[]string{"environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"LOG_LEVEL":                 "${AZURE_LOG_LEVEL}",
		"MODEL_ENDPOINT":            "$${{project.endpoint}}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

// TestPersistInlineAgentEnvironmentEscapesLegacyFoundrySpan verifies
// a legacy environmentVariables value carrying a raw Foundry ${{...}}
// span is escaped to $${{...}} when migrated into the env section.
func TestPersistInlineAgentEnvironmentEscapesLegacyFoundrySpan(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "SEARCH_KEY",
			"value": "${{connections.search.credentials.key}}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}

	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(
		t,
		[]string{"environmentVariables"},
		server.unsetPaths,
	)
	require.Equal(t, map[string]any{
		"SEARCH_KEY":                "$${{connections.search.credentials.key}}",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

func TestPersistInlineAgentEnvironmentNormalizesScalars(t *testing.T) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
		Environment: map[string]string{
			"LARGE": "9007199254740993",
		},
	}
	server := &recordingProjectServer{
		rawEnv: map[string]map[string]any{
			"basic-agent": {
				"ENABLED": true,
				"RETRIES": float64(3),
				"EMPTY":   nil,
				"LARGE":   float64(9007199254740992),
			},
		},
	}

	client := newProjectRecorderClient(t, server)
	require.NoError(t, persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, map[string]any{
		"ENABLED":                   "true",
		"RETRIES":                   "3",
		"EMPTY":                     "",
		"LARGE":                     "9007199254740993",
		"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
	}, server.env["basic-agent"])
}

func TestPersistInlineAgentEnvironmentKeepsLegacyOnEnvFailure(
	t *testing.T,
) {
	props, err := projectpkg.AgentDefinitionToServiceProperties(
		agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindHosted,
				Name: "basic-agent",
			},
			Protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "2.0.0"},
			},
		},
		nil,
	)
	require.NoError(t, err)
	legacyEnvironment, err := structpb.NewValue([]any{
		map[string]any{
			"name":  "LEGACY_KEY",
			"value": "${LEGACY_KEY}",
		},
	})
	require.NoError(t, err)
	props.Fields["environmentVariables"] = legacyEnvironment
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	server := &recordingProjectServer{
		setEnvironmentErr: fmt.Errorf("write failed"),
	}

	client := newProjectRecorderClient(t, server)
	err = persistInlineAgentEnvironment(
		t.Context(),
		client,
		svc,
		map[string]string{"OPTIMIZATION_CANDIDATE_ID": "candidate-1"},
	)

	require.ErrorContains(t, err, "write failed")
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.unsetPaths)
	require.Empty(t, server.added)
}

func TestPersistPromptAgentCandidateConfig(t *testing.T) {
	t.Parallel()

	for _, legacy := range []bool{false, true} {
		for _, instructionKey := range []string{"system_prompt", "systemPrompt", "instructions"} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, instructionKey), func(t *testing.T) {
				t.Parallel()
				svc := newPromptCandidateTestService(t, legacy)
				server := &recordingProjectServer{}
				client := newProjectRecorderClient(t, server)
				tools := []any{map[string]any{"type": "code_interpreter"}}

				require.NoError(t, persistPromptAgentCandidateConfig(
					t.Context(), client, svc, t.TempDir(), mustMarshal(t, map[string]any{
						"model":        "gpt-5",
						instructionKey: "Optimized instructions.",
						"tools":        tools,
						"skills":       []any{map[string]any{"name": "unsupported-prompt-skill"}},
					}),
				))

				prefix := ""
				if legacy {
					prefix = "config."
				}
				server.mu.Lock()
				defer server.mu.Unlock()
				require.Equal(t, map[string]configValueRecord{
					prefix + "model":        {serviceName: svc.Name, value: "gpt-5"},
					prefix + "instructions": {serviceName: svc.Name, value: "Optimized instructions."},
					prefix + "tools":        {serviceName: svc.Name, value: tools},
				}, server.configValues)
				require.Empty(t, server.unsetPaths)
			})
		}
	}
}

func TestPersistPromptAgentCandidateConfigOptionalTools(t *testing.T) {
	t.Parallel()

	for _, legacy := range []bool{false, true} {
		for _, toolsCase := range []string{"missing", "null", "empty"} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, toolsCase), func(t *testing.T) {
				t.Parallel()
				svc := newPromptCandidateTestService(t, legacy)
				server := &recordingProjectServer{}
				client := newProjectRecorderClient(t, server)
				config := map[string]any{"model": "gpt-5", "instructions": "Baseline instructions."}
				switch toolsCase {
				case "null":
					config["tools"] = nil
				case "empty":
					config["tools"] = []any{}
				}

				require.NoError(t, persistPromptAgentCandidateConfig(
					t.Context(), client, svc, t.TempDir(), mustMarshal(t, config),
				))

				prefix := ""
				if legacy {
					prefix = "config."
				}
				server.mu.Lock()
				defer server.mu.Unlock()
				require.Contains(t, server.configValues, prefix+"model")
				require.Contains(t, server.configValues, prefix+"instructions")
				require.Equal(t, "gpt-5", server.configValues[prefix+"model"].value)
				require.Equal(t, "Baseline instructions.", server.configValues[prefix+"instructions"].value)
				if toolsCase == "empty" {
					require.Contains(t, server.configValues, prefix+"tools")
					require.Equal(t, []any{}, server.configValues[prefix+"tools"].value)
					require.Empty(t, server.unsetPaths)
				} else {
					require.NotContains(t, server.configValues, prefix+"tools")
					require.Equal(t, []string{prefix + "tools"}, server.unsetPaths)
				}
			})
		}
	}
}

func TestPersistPromptAgentCandidateConfigRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config string
		err    string
	}{
		{"invalid JSON", `{`, "failed to parse candidate config"},
		{"missing model", `{"instructions":"Keep this."}`, "does not contain a model"},
		{"null model", `{"model":null,"instructions":"Keep this."}`, "does not contain a model"},
		{"empty model", `{"model":"","instructions":"Keep this."}`, "invalid model"},
		{"whitespace model", `{"model":" \t","instructions":"Keep this."}`, "invalid model"},
		{"non-string model", `{"model":42,"instructions":"Keep this."}`, "invalid model"},
		{"missing instructions", `{"model":"gpt-5"}`, "does not contain non-empty instructions"},
		{"null instructions", `{"model":"gpt-5","instructions":null}`, "does not contain non-empty instructions"},
		{"empty instructions", `{"model":"gpt-5","instructions":""}`, "does not contain non-empty instructions"},
		{"whitespace instructions", `{"model":"gpt-5","instructions":" \t\n"}`, "does not contain non-empty instructions"},
		{"numeric instructions", `{"model":"gpt-5","instructions":42}`, "does not contain non-empty instructions"},
		{"object instructions", `{"model":"gpt-5","instructions":{}}`, "does not contain non-empty instructions"},
		{"array instructions", `{"model":"gpt-5","instructions":[]}`, "does not contain non-empty instructions"},
		{"null preferred alias", `{"model":"gpt-5","system_prompt":null,"instructions":"Fallback"}`,
			"does not contain non-empty instructions"},
		{"non-array tools", `{"model":"gpt-5","instructions":"Keep this.","tools":{}}`, "tools must be an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := &recordingProjectServer{}
			client := newProjectRecorderClient(t, server)
			err := persistPromptAgentCandidateConfig(
				t.Context(), client, newPromptCandidateTestService(t, false), t.TempDir(), json.RawMessage(tt.config),
			)
			require.ErrorContains(t, err, tt.err)
			server.mu.Lock()
			defer server.mu.Unlock()
			require.Empty(t, server.configValues)
			require.Empty(t, server.unsetPaths)
			require.Empty(t, server.env)
		})
	}
}

func newPromptCandidateTestService(t *testing.T, legacy bool) *azdext.ServiceConfig {
	t.Helper()
	props, err := projectpkg.PromptAgentDefinitionToServiceProperties(
		agent_yaml.PromptAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindPrompt,
				Name: "prompt-agent",
			},
			Model:        "gpt-4.1-mini",
			Instructions: "Original instructions.",
			Tools:        []any{map[string]any{"type": "code_interpreter"}},
		},
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "prompt-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	if legacy {
		svc.Config = props
		svc.AdditionalProperties = nil
	}
	return svc
}

func TestPersistPromptAgentCandidateConfigSkipsVoiceAgent(t *testing.T) {
	t.Parallel()

	props, err := projectpkg.VoiceAgentDefinitionToServiceProperties(
		agent_yaml.VoiceAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind: agent_yaml.AgentKindPromptVoice,
				Name: "voice-agent",
			},
			Model: &agent_yaml.Model{Id: "gpt-realtime"},
		},
		nil,
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "voice-agent",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	server := &recordingProjectServer{}
	client := newProjectRecorderClient(t, server)

	require.NoError(t, persistPromptAgentCandidateConfig(
		t.Context(),
		client,
		svc,
		t.TempDir(),
		json.RawMessage(`not-json`),
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues)
}

func TestOptimizeApply_PersistsCandidateByAgentKind(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		legacy bool
		disk   bool
	}{
		{name: "prompt", kind: "prompt"},
		{name: "legacy prompt", kind: "prompt", legacy: true},
		{name: "hosted", kind: "hosted"},
		{name: "voice", kind: "prompt-voice"},
		{name: "file-backed hosted", kind: "hosted", disk: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			svc := newPromptCandidateTestService(t, tt.legacy)
			svc.RelativePath = "."
			if tt.kind != "prompt" {
				values := map[string]any{"kind": tt.kind, "name": svc.Name}
				if tt.kind == "prompt-voice" {
					values["model"] = map[string]any{"id": "gpt-realtime"}
				}
				props, err := structpb.NewStruct(values)
				require.NoError(t, err)
				svc.AdditionalProperties = props
			}
			svc.Environment = map[string]string{"CUSTOM_SETTING": "keep"}
			root := t.TempDir()
			if tt.disk {
				svc.AdditionalProperties = nil
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
					[]byte("kind: hosted\nname: prompt-agent\n"), 0600))
			}
			projectServer := &recordingProjectServer{
				rawEnv: map[string]map[string]any{svc.Name: {"CUSTOM_SETTING": "${CUSTOM_SETTING}"}},
			}
			envServer := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
				values: map[string]map[string]string{
					"dev": {optimizeJobIDKeyForAgent(svc.Name): "opt-1"},
				},
			}
			address := newProjectRecorderServer(t, projectServer, envServer)
			t.Setenv("AZD_SERVER", address)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })

			requests := make(chan string, 4)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/agent_optimization_jobs/opt-1/candidates/candidate-1/config":
					_, err := w.Write([]byte(`{"model":"gpt-5","system_prompt":"Optimized instructions.","tools":[]}`))
					assert.NoError(t, err)
				case "/agent_optimization_jobs/opt-1/candidates/candidate-1":
					_, err := w.Write([]byte(`{"files":[]}`))
					assert.NoError(t, err)
				default:
					t.Errorf("unexpected API request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(api.Close)
			action := &OptimizeApplyAction{
				flags: &optimizeApplyFlags{
					candidate: "candidate-1", optimizeConnectionFlags: optimizeConnectionFlags{projectEndpoint: api.URL},
				},
				envName: "dev",
				client:  newTestOptimizeClient(api.URL),
			}
			var out bytes.Buffer
			require.NoError(t, action.apply(t.Context(), client, svc,
				&azdext.ProjectConfig{Path: root}, &out, color.New(color.Bold)))
			require.Len(t, requests, 2, "apply must not fetch job status or mutation metadata")
			require.FileExists(t, filepath.Join(root, agentConfigsDir, "candidate-1", opt_eval.MetadataFile))
			require.Equal(t, "candidate-1", envServer.values["dev"]["AGENT_PROMPT_AGENT_OPTIMIZATION_CANDIDATE_ID"])
			require.Contains(t, out.String(), "applied to")
			require.Equal(t, map[string]string{"CUSTOM_SETTING": "keep"}, svc.Environment)

			projectServer.mu.Lock()
			defer projectServer.mu.Unlock()
			if tt.kind == "prompt" {
				prefix := ""
				if tt.legacy {
					prefix = "config."
				}
				require.Equal(t, map[string]configValueRecord{
					prefix + "model":        {serviceName: svc.Name, value: "gpt-5"},
					prefix + "instructions": {serviceName: svc.Name, value: "Optimized instructions."},
					prefix + "tools":        {serviceName: svc.Name, value: []any{}},
				}, projectServer.configValues)
				require.Empty(t, projectServer.env)
				require.Empty(t, projectServer.unsetPaths)
			} else if tt.disk {
				content, err := os.ReadFile(filepath.Join(root, "agent.yaml"))
				require.NoError(t, err)
				require.Contains(t, string(content), "OPTIMIZATION_LOCAL_DIR")
				require.Contains(t, string(content), "candidate-1")
				require.Empty(t, projectServer.configValues)
			} else {
				require.Empty(t, projectServer.configValues)
				require.Equal(t, map[string]any{
					"CUSTOM_SETTING":            "${CUSTOM_SETTING}",
					"OPTIMIZATION_LOCAL_DIR":    agentConfigsDir,
					"OPTIMIZATION_CANDIDATE_ID": "candidate-1",
				}, projectServer.env[svc.Name])
			}
		})
	}
}

func TestOptimizeApply_InvalidPromptCandidateDoesNotWrite(t *testing.T) {
	svc := newPromptCandidateTestService(t, false)
	root := t.TempDir()
	server := &recordingProjectServer{}
	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
		values: map[string]map[string]string{
			"dev": {
				optimizeJobIDKeyForAgent(svc.Name):             "opt-1",
				"AGENT_PROMPT_AGENT_OPTIMIZATION_CANDIDATE_ID": "previous",
			},
		},
	}
	t.Setenv("AZD_SERVER", newProjectRecorderServer(t, server, envServer))
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/opt-1/candidates/candidate-1/config", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"model":"gpt-5"}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(api.Close)
	action := &OptimizeApplyAction{
		flags: &optimizeApplyFlags{
			candidate: "candidate-1", optimizeConnectionFlags: optimizeConnectionFlags{projectEndpoint: api.URL},
		},
		envName: "dev",
		client:  newTestOptimizeClient(api.URL),
	}
	var out bytes.Buffer
	err = action.apply(t.Context(), client, svc, &azdext.ProjectConfig{Path: root}, &out, color.New(color.Bold))
	require.ErrorContains(t, err, "does not contain non-empty instructions")
	require.NoDirExists(t, filepath.Join(root, agentConfigsDir))
	require.Empty(t, envServer.setKeys)
	require.Equal(t, "previous", envServer.values["dev"]["AGENT_PROMPT_AGENT_OPTIMIZATION_CANDIDATE_ID"])
	require.NotContains(t, out.String(), "applied to")
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Empty(t, server.configValues)
	require.Empty(t, server.unsetPaths)
	require.Empty(t, server.env)
}

func TestOptimizeApply_ReferencedDefinitionGuidance(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"prompt", "hosted"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			definition := fmt.Sprintf("kind: %s\nname: assistant\nmodel: gpt-5\ninstructions: Be helpful.\n", kind)
			definitionPath := filepath.Join(root, "assistant.yaml")
			require.NoError(t, os.WriteFile(definitionPath, []byte(definition), 0600))
			props, err := structpb.NewStruct(map[string]any{"$ref": "assistant.yaml"})
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost, AdditionalProperties: props}
			server := &recordingProjectServer{}
			client := newProjectRecorderClient(t, server)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("referenced definition must fail before calling optimization API: %s", r.URL.Path)
				http.NotFound(w, r)
			}))
			t.Cleanup(api.Close)
			action := &OptimizeApplyAction{
				flags: &optimizeApplyFlags{
					candidate: "candidate-1", optimizeConnectionFlags: optimizeConnectionFlags{projectEndpoint: api.URL},
				},
				client: newTestOptimizeClient(api.URL),
			}
			var out bytes.Buffer
			err = action.apply(t.Context(), client, svc, &azdext.ProjectConfig{Path: root}, &out, color.New(color.Bold))
			require.ErrorContains(t, err, `agent service "assistant" defines its agent via $ref`)
			if kind == "prompt" {
				require.ErrorContains(t, err, "Inline the definition in azure.yaml and rerun 'optimize apply'")
				require.ErrorContains(t, err, "manually update model, instructions, and tools in the referenced file")
				require.NotContains(t, err.Error(), "OPTIMIZATION_")
			} else {
				require.ErrorContains(t, err, "Add OPTIMIZATION_LOCAL_DIR and OPTIMIZATION_CANDIDATE_ID")
			}
			require.NoDirExists(t, filepath.Join(root, agentConfigsDir))
			content, readErr := os.ReadFile(definitionPath)
			require.NoError(t, readErr)
			require.Equal(t, definition, string(content))
			require.Empty(t, out.String())
			server.mu.Lock()
			defer server.mu.Unlock()
			require.Empty(t, server.configValues)
			require.Empty(t, server.unsetPaths)
			require.Empty(t, server.env)
		})
	}
}

// ---- printPreviewLines ----

func TestPrintPreviewLines(t *testing.T) {
	t.Parallel()

	// Disable color output so assertions don't need ANSI codes.
	color.NoColor = true

	tests := []struct {
		name   string
		lines  []string
		prefix string
		want   []string // substrings expected in output
	}{
		{
			"fewer lines than limit",
			[]string{"line1", "line2"},
			"+ ",
			[]string{"+ line1", "+ line2"},
		},
		{
			"exactly at limit",
			[]string{"a", "b", "c", "d"},
			"- ",
			[]string{"- a", "- b", "- c", "- d"},
		},
		{
			"exceeds limit shows truncation",
			[]string{"a", "b", "c", "d", "e", "f"},
			"+ ",
			[]string{"+ a", "+ b", "+ c", "+ d", "... (2 more lines)"},
		},
		{
			"empty lines",
			[]string{},
			"- ",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			c := color.New(color.FgWhite)
			printPreviewLines(&buf, tt.lines, tt.prefix, c)
			out := buf.String()
			for _, s := range tt.want {
				assert.Contains(t, out, s)
			}
			if tt.want == nil {
				assert.Empty(t, out)
			}
		})
	}
}

// ---- printPromptDiff ----

func TestPrintPromptDiff(t *testing.T) {
	t.Parallel()

	color.NoColor = true

	t.Run("shows diff when baseline and candidate have instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Set up baseline with metadata that points to an instruction file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opt_eval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.InstructionFile),
			[]byte("You are a baseline assistant.\nLine two."),
			0600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.MetadataFile),
			[]byte("instruction_file: instructions.md\nmodel: gpt-4o\n"),
			0600,
		))

		candidateConfig := mustMarshal(t, map[string]any{
			"systemPrompt": "You are an optimized assistant.\nNew line two.\nNew line three.",
		})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		out := buf.String()

		assert.Contains(t, out, "Instruction diff")
		assert.Contains(t, out, "Baseline")
		assert.Contains(t, out, "Optimized")
		assert.Contains(t, out, "You are a baseline assistant.")
		assert.Contains(t, out, "You are an optimized assistant.")
	})

	t.Run("no output when candidate has no instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := mustMarshal(t, map[string]any{"model": "gpt-4o"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline config missing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := mustMarshal(t, map[string]any{"systemPrompt": "optimized"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline has no instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Write metadata without instruction_file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opt_eval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opt_eval.MetadataFile),
			[]byte("model: gpt-4o\n"),
			0600,
		))

		candidateConfig := mustMarshal(t, map[string]any{"systemPrompt": "optimized"})

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// ---- extractInstructions ----

func TestExtractInstructions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			"systemPrompt field",
			map[string]any{"systemPrompt": "You are a helpful assistant."},
			"You are a helpful assistant.",
		},
		{
			"system_prompt field (snake_case)",
			map[string]any{"system_prompt": "Snake-case prompt."},
			"Snake-case prompt.",
		},
		{
			"system_prompt takes precedence over camelCase",
			map[string]any{
				"system_prompt": "From snake_case",
				"systemPrompt":  "From camelCase",
			},
			"From snake_case",
		},
		{
			"instructions field",
			map[string]any{"instructions": "Follow the rules."},
			"Follow the rules.",
		},
		{
			"systemPrompt takes precedence",
			map[string]any{
				"systemPrompt": "From systemPrompt",
				"instructions": "From instructions",
			},
			"From systemPrompt",
		},
		{"nil config", nil, ""},
		{"empty map", map[string]any{}, ""},
		{"non-string value", map[string]any{"systemPrompt": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractInstructions(tt.config))
		})
	}
}

// ---- agentConfigMetadata.resolveInstructions ----

func TestAgentConfigMetadata_ResolveInstructions(t *testing.T) {
	t.Parallel()
	t.Run("reads instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Be helpful."), 0600))

		meta := &agentConfigMetadata{InstructionFile: "instructions.md"}
		assert.Equal(t, "Be helpful.", meta.resolveInstructions(dir))
	})

	t.Run("returns empty when no file set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})

	t.Run("returns empty when file missing", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{InstructionFile: "nonexistent.md"}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})
}

// ---- agentConfigMetadata.resolveSkillDir ----

func TestAgentConfigMetadata_ResolveSkillDir(t *testing.T) {
	t.Parallel()
	t.Run("returns empty when not set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveSkillDir("/some/dir"))
	})

	t.Run("resolves relative path", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{SkillDir: "skills"}
		result := meta.resolveSkillDir("/project/config")
		assert.Equal(t, filepath.Join("/project/config", "skills"), result)
	})

	t.Run("preserves absolute path", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "absolute-skills")
		meta := &agentConfigMetadata{SkillDir: abs}
		assert.Equal(t, abs, meta.resolveSkillDir("/any/dir"))
	})
}

func TestAgentConfigMetadata_ResolveToolsFile(t *testing.T) {
	t.Parallel()
	t.Run("returns empty when not set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveToolsFile("/some/dir"))
	})

	t.Run("resolves relative path", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{ToolsFile: "tools.json"}
		result := meta.resolveToolsFile("/project/config")
		assert.Equal(t, filepath.Join("/project/config", "tools.json"), result)
	})

	t.Run("preserves absolute path", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "absolute-tools.json")
		meta := &agentConfigMetadata{ToolsFile: abs}
		assert.Equal(t, abs, meta.resolveToolsFile("/any/dir"))
	})
}

// ---- writeAgentConfigFromCandidate ----

func TestWriteAgentConfigFromCandidate(t *testing.T) {
	t.Parallel()
	t.Run("writes metadata and instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := mustMarshal(t, map[string]any{
			"name":         "test-agent",
			"model":        "gpt-4o",
			"systemPrompt": "Test prompt.",
		})

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
		assert.FileExists(t, filepath.Join(dir, opt_eval.InstructionFile))

		content, err := os.ReadFile(filepath.Join(dir, opt_eval.InstructionFile)) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Equal(t, "Test prompt.", string(content))
	})

	t.Run("writes inline skills", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := mustMarshal(t, map[string]any{
			"systemPrompt": "prompt",
			"skills": []any{
				map[string]any{
					"name":        "search",
					"description": "Search the web",
					"body":        "Search content here.",
				},
			},
		})

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		skillFile := filepath.Join(dir, opt_eval.SkillsDir, "search", "SKILL.md")
		assert.FileExists(t, skillFile)
	})

	t.Run("handles nil config gracefully", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeAgentConfigFromCandidate(dir, json.RawMessage(`{}`))
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
	})
}

// ---- isSkillFile ----

func TestIsSkillFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file optimize_api.CandidateFile
		want bool
	}{
		{"skill type", optimize_api.CandidateFile{Type: "skill", Path: "foo.md"}, true},
		{"skills path prefix", optimize_api.CandidateFile{Type: "file", Path: "skills/search/SKILL.md"}, true},
		{"other type and path", optimize_api.CandidateFile{Type: "file", Path: "config.yaml"}, false},
		{"empty", optimize_api.CandidateFile{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSkillFile(tt.file))
		})
	}
}

// ---- isReservedEnvVarError ----

func TestIsReservedEnvVarError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"reserved for platform use", fmt.Errorf("variable is reserved for platform use"), true},
		{"AGENT_* variables", fmt.Errorf("AGENT_* variables are reserved"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isReservedEnvVarError(tt.err))
		})
	}
}

// ---- writeToolsFile ----

func TestWriteToolsFile_NoTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := writeToolsFile(dir, map[string]any{"name": "agent"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

func TestWriteToolsFile_WithTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tools := []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
	}
	err := writeToolsFile(dir, map[string]any{"tools": tools})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, opt_eval.ToolsFile)) //nolint:gosec // test file path
	require.NoError(t, err)

	var parsed []any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Len(t, parsed, 1)
}

func TestWriteAgentConfigFromCandidate_WithTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"systemPrompt": "prompt",
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "lookup_travel_policy"}},
		},
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, opt_eval.ToolsFile))

	// Verify metadata references tools_file.
	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(metaData), "tools_file")
}

// ---- writeToolsFile: ignores legacy keys ----

func TestWriteToolsFile_IgnoresToolDefinitions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// toolDefinitions is no longer supported — should not produce a file.
	err := writeToolsFile(dir, map[string]any{
		"toolDefinitions": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search"}},
		},
	})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

func TestWriteToolsFile_IgnoresToolDescriptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// toolDescriptions is no longer supported — should not produce a file.
	err := writeToolsFile(dir, map[string]any{
		"toolDescriptions": map[string]any{
			"lookup": map[string]any{"description": "Look up stuff"},
		},
	})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
}

// ---- writeAgentConfigFromCandidate: model in metadata ----

func TestWriteAgentConfigFromCandidate_ModelInMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"model":        "gpt-4o",
		"instructions": "Approve travel.",
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Contains(t, string(metaData), "model: gpt-4o")
}

func TestWriteAgentConfigFromCandidate_NoModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"instructions": "Approve travel.",
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	// model is omitempty, so it should not appear when not set.
	assert.NotContains(t, string(metaData), "model:")
}

// ---- writeAgentConfigFromCandidate: full candidate config ----

func TestWriteAgentConfigFromCandidate_FullConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	config := mustMarshal(t, map[string]any{
		"name":         "travel-approver",
		"model":        "gpt-4o",
		"instructions": "Review and approve travel requests.",
		"skills": []any{
			map[string]any{
				"name":        "policy-reviewer",
				"description": "Reviews travel requests",
				"body":        "# Policy Reviewer\nApprove everything.",
			},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "lookup_travel_policy",
					"description": "Look up travel policy",
				},
			},
		},
	})

	err := writeAgentConfigFromCandidate(dir, config)
	require.NoError(t, err)

	// Verify all files are created.
	assert.FileExists(t, filepath.Join(dir, opt_eval.MetadataFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.InstructionFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.ToolsFile))
	assert.FileExists(t, filepath.Join(dir, opt_eval.SkillsDir, "policy-reviewer", "SKILL.md"))

	// Verify metadata has all fields.
	metaData, err := os.ReadFile(filepath.Join(dir, opt_eval.MetadataFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	metaStr := string(metaData)
	assert.Contains(t, metaStr, "model: gpt-4o")
	assert.Contains(t, metaStr, "instruction_file")
	assert.Contains(t, metaStr, "skill_dir")
	assert.Contains(t, metaStr, "tools_file")

	// Verify instructions content.
	instrData, err := os.ReadFile(filepath.Join(dir, opt_eval.InstructionFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	assert.Equal(t, "Review and approve travel requests.", string(instrData))

	// Verify tools.json is a list.
	toolsData, err := os.ReadFile(filepath.Join(dir, opt_eval.ToolsFile)) //nolint:gosec // test file path
	require.NoError(t, err)
	var toolsParsed []any
	require.NoError(t, json.Unmarshal(toolsData, &toolsParsed))
	assert.Len(t, toolsParsed, 1)
}
