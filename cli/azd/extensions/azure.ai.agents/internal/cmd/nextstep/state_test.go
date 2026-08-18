// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeSource is a hand-rolled Source for table-driven tests.
type fakeSource struct {
	envName      string
	envNameErr   error
	project      *azdext.ProjectConfig
	projectErr   error
	values       map[string]string
	valueErr     error
	valueErrors  map[string]error
	configValues map[string]*structpb.Value
	configErrors map[string]error
	calls        map[string]int
}

func (f *fakeSource) CurrentEnvName(_ context.Context) (string, error) {
	return f.envName, f.envNameErr
}

func (f *fakeSource) Project(_ context.Context) (*azdext.ProjectConfig, error) {
	return f.project, f.projectErr
}

func (f *fakeSource) EnvValue(_ context.Context, envName, key string) (string, error) {
	if f.calls != nil {
		f.calls[envName+"/"+key]++
	}
	if f.valueErr != nil {
		return "", f.valueErr
	}
	if err := f.valueErrors[envName+"/"+key]; err != nil {
		return "", err
	}

	return f.values[envName+"/"+key], nil
}

func (f *fakeSource) ServiceConfigValue(
	_ context.Context,
	serviceName string,
	path string,
) (*structpb.Value, bool, error) {
	key := serviceName + "/" + path
	if err := f.configErrors[key]; err != nil {
		return nil, false, err
	}
	value, found := f.configValues[key]
	return value, found, nil
}

func TestAssembleState_SplitToolboxesProbeCanonicalEndpoints(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		values: map[string]string{
			"dev/TOOLBOX_ALPHA_MCP_ENDPOINT":      "https://alpha.example/mcp",
			"dev/TOOLBOX_ALPHA_COPY_MCP_ENDPOINT": "https://alpha-copy.example/mcp",
		},
		calls: make(map[string]int),
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"alpha": {
					Name: "alpha", Host: "azure.ai.toolbox",
				},
				"alpha-copy": {
					Name: "alpha-copy", Host: "azure.ai.toolbox",
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"alpha", "alpha-copy"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.True(t, state.ToolboxEndpointsChecked)
	require.Len(t, state.Toolboxes, 2)
	require.Equal(t, ToolboxSourceSplit, state.Toolboxes[0].ToolboxSource)
	require.Equal(t, "alpha", state.Toolboxes[0].Name)
	require.Empty(t, state.MissingToolboxEndpoints)
	require.Equal(t, 1, src.calls["dev/TOOLBOX_ALPHA_MCP_ENDPOINT"])
	require.Equal(t, 1, src.calls["dev/TOOLBOX_ALPHA_COPY_MCP_ENDPOINT"])
}

func TestAssembleState_SplitToolboxMissingEndpointIsNotManual(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		values: map[string]string{
			"dev/TOOLBOX_OTHER_MCP_ENDPOINT": "https://other.example/mcp",
		},
		configErrors: map[string]error{
			"missing/condition": errors.New("unreferenced condition must not be read"),
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"missing": {
					Name: "missing", Host: "azure.ai.toolbox",
				},
				"other": {
					Name: "other", Host: "azure.ai.toolbox",
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"other"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Empty(t, state.MissingManualVars)
	require.Empty(t, state.MissingToolboxEndpoints)
	require.Len(t, state.Toolboxes, 1)
	require.Equal(t, "other", state.Toolboxes[0].Name)
	require.Equal(t, 0, src.calls["dev/TOOLBOX_MISSING_MCP_ENDPOINT"])
}

func TestAssembleState_SplitToolboxEndpointErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("grpc: connection refused")
	src := &fakeSource{
		envName: "dev",
		valueErrors: map[string]error{
			"dev/TOOLBOX_MISSING_MCP_ENDPOINT": wantErr,
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"missing": {
					Name: "missing", Host: "azure.ai.toolbox",
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"missing"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.True(t, state.ToolboxEndpointsChecked)
	require.Empty(t, state.MissingToolboxEndpoints)
	require.Equal(t,
		[]string{"read toolbox endpoint TOOLBOX_MISSING_MCP_ENDPOINT: grpc: connection refused"},
		state.ToolboxEndpointErrors,
	)
	require.Len(t, errs, 1)
	require.ErrorContains(t, errs[0], "grpc: connection refused")
}

func TestPopulateSplitToolboxes_PrefersSplitCanonicalKey(t *testing.T) {
	t.Parallel()

	state := &State{
		Toolboxes: []ResourceRef{
			{Name: "my+tool", ServiceName: "agent", ToolboxSource: ToolboxSourceLegacyManifest},
			{Name: "legacy", ServiceName: "agent", ToolboxSource: ToolboxSourceLegacyManifest},
		},
	}
	project := &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"my-tool": {Name: "my-tool", Host: "azure.ai.toolbox"},
			"agent": {
				Name: "agent", Host: agentHost,
				Uses: []string{"my-tool"},
			},
		},
	}

	var errs []error
	populateSplitToolboxes(
		t.Context(),
		&fakeSource{},
		"",
		project,
		state,
		&errs,
	)
	require.Empty(t, errs)
	require.Len(t, state.Toolboxes, 2)
	require.Equal(t, "legacy", state.Toolboxes[0].Name)
	require.Equal(t, ToolboxSourceLegacyManifest, state.Toolboxes[0].ToolboxSource)
	require.Equal(t, "my-tool", state.Toolboxes[1].Name)
	require.Equal(t, ToolboxSourceSplit, state.Toolboxes[1].ToolboxSource)
}

func TestAssembleState_SplitToolboxConditionDisabled(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"disabled/condition": structpb.NewBoolValue(false),
		},
		calls: make(map[string]int),
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"disabled": {
					Name: "disabled", Host: toolboxHost,
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"disabled"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Len(t, errs, 1)
	require.False(t, state.HasToolboxes)
	require.Empty(t, state.MissingToolboxEndpoints)
	require.Len(t, state.ToolboxDependencyErrors, 1)
	require.Contains(t, state.ToolboxDependencyErrors[0], "disabled")
	require.Equal(t, 0, src.calls["dev/TOOLBOX_DISABLED_MCP_ENDPOINT"])
}

func TestAssembleState_SplitToolboxConditionUsesEnvironment(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		values: map[string]string{
			"dev/ENABLE_TOOLBOX": "false",
		},
		configValues: map[string]*structpb.Value{
			"conditional/condition": structpb.NewStringValue("${ENABLE_TOOLBOX}"),
		},
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"conditional": {
					Name: "conditional", Host: toolboxHost,
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"conditional"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Len(t, errs, 1)
	require.Empty(t, state.Toolboxes)
	require.Len(t, state.ToolboxDependencyErrors, 1)
	require.Contains(t, state.ToolboxDependencyErrors[0], "conditional")
}

func TestAssembleState_SplitToolboxConditionErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		configValues: map[string]*structpb.Value{
			"malformed/condition": structpb.NewStringValue("${"),
		},
		calls: make(map[string]int),
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"malformed": {
					Name: "malformed", Host: toolboxHost,
				},
				"agent": {
					Name: "agent", Host: agentHost,
					Uses: []string{"malformed"},
				},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Len(t, errs, 1)
	require.Empty(t, state.Toolboxes)
	require.Len(t, state.ToolboxDependencyErrors, 1)
	require.Contains(t, state.ToolboxDependencyErrors[0], "invalid deployment condition")
	require.Equal(t, 0, src.calls["dev/TOOLBOX_MALFORMED_MCP_ENDPOINT"])
}

func TestAssembleState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      *fakeSource
		assert   func(t *testing.T, state *State, errs []error)
		errCount int
	}{
		{
			name: "no project, no env: state is empty and errors are surfaced",
			src: &fakeSource{
				envNameErr: errors.New("no env"),
				projectErr: errors.New("no project"),
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.False(t, state.HasProjectEndpoint)
				assert.Empty(t, state.Services)
			},
			errCount: 2,
		},
		{
			name: "endpoint set, no services: HasProjectEndpoint true",
			src: &fakeSource{
				envName: "dev",
				values:  map[string]string{"dev/FOUNDRY_PROJECT_ENDPOINT": "https://x.services.ai.azure.com"},
				project: &azdext.ProjectConfig{Name: "demo"},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.True(t, state.HasProjectEndpoint)
				assert.Empty(t, state.Services)
				assert.Equal(t,
					[]string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
					state.MissingAzureContextVars,
				)
			},
		},
		{
			name: "Azure context vars set: MissingAzureContextVars stays empty",
			src: &fakeSource{
				envName: "dev",
				values: map[string]string{
					"dev/AZURE_SUBSCRIPTION_ID":    "sub-id",
					"dev/AZURE_LOCATION":           "eastus",
					"dev/FOUNDRY_PROJECT_ENDPOINT": "https://x.services.ai.azure.com",
				},
				project: &azdext.ProjectConfig{Name: "demo"},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.Empty(t, state.MissingAzureContextVars)
			},
		},
		{
			name: "endpoint unset, one undeployed service",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"echo": {Name: "echo", Host: agentHost, RelativePath: "./src/echo"},
					},
				},
				values: map[string]string{},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.False(t, state.HasProjectEndpoint)
				require.Len(t, state.Services, 1)
				assert.Equal(t, "echo", state.Services[0].Name)
				assert.Equal(t, agentHost, state.Services[0].Host)
				assert.Equal(t, "./src/echo", state.Services[0].RelativePath)
				assert.False(t, state.Services[0].IsDeployed)
			},
		},
		{
			name: "multiple services: deployed flag follows AGENT_<KEY>_VERSION, alphabetical order",
			src: &fakeSource{
				envName: "prod",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"chat-bot":   {Name: "chat-bot", Host: agentHost},
						"echo":       {Name: "echo", Host: agentHost},
						"my service": {Name: "my service", Host: agentHost},
					},
				},
				values: map[string]string{
					"prod/AGENT_CHAT_BOT_VERSION":   "1",
					"prod/AGENT_MY_SERVICE_VERSION": "7",
					// echo has no VERSION → not deployed
				},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				require.Len(t, state.Services, 3)
				assert.Equal(t, "chat-bot", state.Services[0].Name)
				assert.True(t, state.Services[0].IsDeployed)
				assert.Equal(t, "echo", state.Services[1].Name)
				assert.False(t, state.Services[1].IsDeployed)
				assert.Equal(t, "my service", state.Services[2].Name)
				assert.True(t, state.Services[2].IsDeployed)
			},
		},
		{
			name: "non-agent services are filtered out",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"echo":   {Name: "echo", Host: agentHost},
						"web":    {Name: "web", Host: "appservice"},
						"worker": {Name: "worker", Host: "containerapp"},
					},
				},
				values: map[string]string{
					"dev/AGENT_ECHO_VERSION": "1",
				},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				require.Len(t, state.Services, 1)
				assert.Equal(t, "echo", state.Services[0].Name)
				assert.Equal(t, agentHost, state.Services[0].Host)
				assert.True(t, state.Services[0].IsDeployed)
			},
		},
		{
			name: "transport error on env-value read does not abort assembly",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
				},
				valueErr: errors.New("gRPC unavailable"),
			},
			assert: func(t *testing.T, state *State, _ []error) {
				require.Len(t, state.Services, 1)
				assert.False(t, state.Services[0].IsDeployed)
				assert.False(t, state.HasProjectEndpoint)
				assert.Empty(t, state.PendingProvisionReasons)
			},
			// One error each for FOUNDRY_PROJECT_ENDPOINT,
			// AI_AGENT_PENDING_PROVISION, AZURE_SUBSCRIPTION_ID,
			// AZURE_LOCATION + one per service lookup
			// (AGENT_ECHO_VERSION) = 5.
			errCount: 5,
		},
		{
			name: "AI_AGENT_PENDING_PROVISION unset: PendingProvisionReasons stays empty",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{Name: "demo"},
				values:  map[string]string{"dev/FOUNDRY_PROJECT_ENDPOINT": "https://x.services.ai.azure.com"},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.Empty(t, state.PendingProvisionReasons)
			},
		},
		{
			name: "AI_AGENT_PENDING_PROVISION single tag: PendingProvisionReasons populated",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{Name: "demo"},
				values: map[string]string{
					"dev/FOUNDRY_PROJECT_ENDPOINT":   "https://x.services.ai.azure.com",
					"dev/AI_AGENT_PENDING_PROVISION": "model_deployment",
				},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.Equal(t, []string{"model_deployment"}, state.PendingProvisionReasons)
			},
		},
		{
			name: "AI_AGENT_PENDING_PROVISION multiple tags: parsed sorted dedup",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{Name: "demo"},
				values: map[string]string{
					"dev/FOUNDRY_PROJECT_ENDPOINT":   "https://x.services.ai.azure.com",
					"dev/AI_AGENT_PENDING_PROVISION": "project,acr,project,model_deployment",
				},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.Equal(t, []string{"acr", "model_deployment", "project"}, state.PendingProvisionReasons)
			},
		},
		{
			name: "AI_AGENT_PENDING_PROVISION malformed value: best-effort normalize",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{Name: "demo"},
				values: map[string]string{
					"dev/FOUNDRY_PROJECT_ENDPOINT":   "https://x.services.ai.azure.com",
					"dev/AI_AGENT_PENDING_PROVISION": "  ,, project ,, acr , ",
				},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.Equal(t, []string{"acr", "project"}, state.PendingProvisionReasons)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state, errs := assembleState(context.Background(), tt.src)
			require.NotNil(t, state)
			assert.Len(t, errs, tt.errCount)
			tt.assert(t, state, errs)
		})
	}
}

func TestAssembleState_WithEnvironmentDoesNotReadCurrent(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName:    "dev",
		envNameErr: errors.New("current environment should not be read"),
		project:    &azdext.ProjectConfig{Name: "demo"},
		values: map[string]string{
			"prod/FOUNDRY_PROJECT_ENDPOINT": "https://x.services.ai.azure.com",
			"prod/AZURE_SUBSCRIPTION_ID":    "sub-id",
			"prod/AZURE_LOCATION":           "eastus",
		},
	}

	state, errs := assembleState(t.Context(), src, WithEnvironment("prod"))
	require.Empty(t, errs)
	assert.Equal(t, "prod", state.EnvironmentName)
	assert.True(t, state.HasProjectEndpoint)
	assert.Empty(t, state.MissingAzureContextVars)
}

func TestAssembleState_NilServiceEntriesAreIgnored(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"good": {Name: "good", Host: agentHost},
				"nil":  nil,
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	assert.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, "good", state.Services[0].Name)
}

func TestAgentHostConstant(t *testing.T) {
	t.Parallel()
	// agentHost must remain in sync with cmd.AiAgentHost ("azure.ai.agent").
	// Pinning the literal here guards against accidental drift while the
	// duplication exists; Phase 2's nextstep wiring should retire it.
	assert.Equal(t, "azure.ai.agent", agentHost)
}

func TestServiceKey(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"echo":         "ECHO",
		"chat-bot":     "CHAT_BOT",
		"my service":   "MY_SERVICE",
		"Mixed-Case 1": "MIXED_CASE_1",
		"":             "",
	}
	for in, want := range tests {
		assert.Equal(t, want, serviceKey(in), "serviceKey(%q)", in)
	}
}

// TestIsDeployed_VoiceEndpointFallback verifies that a voice agent — which sets
// only AGENT_<KEY>_NAME and AGENT_<KEY>_ENDPOINT, never AGENT_<KEY>_VERSION — is
// still reported as deployed via the base endpoint marker, while an agent with
// neither version nor endpoint is reported undeployed.
func TestIsDeployed_VoiceEndpointFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]string
		isVoice bool
		want    bool
	}{
		{
			name:   "version set: deployed (hosted agent)",
			values: map[string]string{"env1/AGENT_VOICE_SVC_VERSION": "1"},
			want:   true,
		},
		{
			name:    "no version but base endpoint set: deployed (voice agent)",
			values:  map[string]string{"env1/AGENT_VOICE_SVC_ENDPOINT": "https://x/voice_agents/a"},
			isVoice: true,
			want:    true,
		},
		{
			name: "hosted agent with lingering endpoint but no version: undeployed",
			// A partially-failed hosted deploy can present an empty VERSION with a
			// stale ENDPOINT. The endpoint fallback must not fire for non-voice
			// services, so it stays reported as not-deployed.
			values:  map[string]string{"env1/AGENT_VOICE_SVC_ENDPOINT": "https://x/agents/a"},
			isVoice: false,
			want:    false,
		},
		{
			name:   "neither version nor endpoint: undeployed",
			values: map[string]string{},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := &fakeSource{values: tc.values}
			var errs []error
			got := isDeployed(t.Context(), src, "env1", "voice-svc", tc.isVoice, &errs)
			assert.Equal(t, tc.want, got)
			assert.Empty(t, errs)
		})
	}
}

func TestOptionsApplyCleanly(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithEnvironment("prod")(cfg)
	WithOpenAPIProbe("echo", "local")(cfg)
	WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) { return nil, nil })(cfg)
	assert.Equal(t, "prod", cfg.environmentName)
	assert.Equal(t, "echo", cfg.openAPIAgent)
	assert.Equal(t, "local", cfg.openAPISuffix)
	assert.NotNil(t, cfg.openAPILiveFetch)
}

func TestWithOpenAPIProbe_EmptyArgsDisableProbe(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithOpenAPIProbe("", "")(cfg)
	assert.Empty(t, cfg.openAPIAgent)
	assert.Empty(t, cfg.openAPISuffix)
}

func TestAssembleState_WithOpenAPIProbe_PopulatesPayloadFromCache(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))

	spec := `{
		"paths": {
			"/invocations": {
				"post": {
					"requestBody": {
						"content": {
							"application/json": {
								"example": {"message": "ping"}
							}
						}
					}
				}
			}
		}
	}`
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "openapi-echo-local.json"),
		[]byte(spec),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost},
			},
		},
	}

	state, errs := assembleState(context.Background(), src, WithOpenAPIProbe("echo", "local"))
	require.Empty(t, errs)
	assert.True(t, state.HasOpenAPI)
	assert.Equal(t, `{"message":"ping"}`, state.OpenAPIPayload)
}

func TestAssembleState_WithOpenAPIProbe_MissingCacheLeavesPayloadUnset(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".azure", "dev"), 0o750))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost},
			},
		},
	}

	state, errs := assembleState(context.Background(), src, WithOpenAPIProbe("echo", "local"))
	require.Empty(t, errs)
	assert.False(t, state.HasOpenAPI)
	assert.Empty(t, state.OpenAPIPayload)
}

func TestAssembleState_WithOpenAPIProbe_DisabledWhenAgentEmpty(t *testing.T) {
	t.Parallel()

	// Lay down a spec that would otherwise be picked up — empty agentName
	// must disable the probe so this cache is ignored.
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "openapi-echo-local.json"),
		[]byte(`{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"x":1}}}}}}}}`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{Path: projectRoot},
	}

	state, errs := assembleState(context.Background(), src, WithOpenAPIProbe("", "local"))
	require.Empty(t, errs)
	assert.False(t, state.HasOpenAPI)
	assert.Empty(t, state.OpenAPIPayload)
}

func TestAssembleState_WithLiveOpenAPIProbe_PrefersLiveOverCache(t *testing.T) {
	t.Parallel()

	// Put a "stale" payload in the on-disk cache. The live probe
	// returns a different payload; the assembler must prefer the
	// live result, proving the live probe takes precedence.
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	stale := `{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"stale":true}}}}}}}}`
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "openapi-echo-local.json"),
		[]byte(stale),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
		},
	}

	fresh := []byte(`{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"fresh":true}}}}}}}}`)
	state, errs := assembleState(
		context.Background(),
		src,
		WithOpenAPIProbe("echo", "local"),
		WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) { return fresh, nil }),
	)
	require.Empty(t, errs)
	assert.True(t, state.HasOpenAPI)
	assert.Equal(t, `{"fresh":true}`, state.OpenAPIPayload)
}

func TestAssembleState_WithLiveOpenAPIProbe_FallsBackToCacheOnError(t *testing.T) {
	t.Parallel()

	// Live probe returns an error; the cache (when present and
	// well-formed) must take over silently — the design budget for
	// the live probe is 3 s and a failed fetch shouldn't deprive
	// the user of the cached sample.
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	cached := `{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"cached":true}}}}}}}}`
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "openapi-echo-local.json"),
		[]byte(cached),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
		},
	}

	state, errs := assembleState(
		context.Background(),
		src,
		WithOpenAPIProbe("echo", "local"),
		WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) {
			return nil, errors.New("connection refused")
		}),
	)
	require.Empty(t, errs)
	assert.True(t, state.HasOpenAPI)
	assert.Equal(t, `{"cached":true}`, state.OpenAPIPayload)
}

func TestAssembleState_WithLiveOpenAPIProbe_FallsBackToCacheOnEmptyBody(t *testing.T) {
	t.Parallel()

	// Live probe returns nil bytes with no error (e.g., agent
	// exposed /openapi.json but the body was empty after read).
	// Treat identically to an error — empty body is unusable for
	// example extraction and the cache must take over.
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	cached := `{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"cached":true}}}}}}}}`
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "openapi-echo-local.json"),
		[]byte(cached),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
		},
	}

	state, errs := assembleState(
		context.Background(),
		src,
		WithOpenAPIProbe("echo", "local"),
		WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) { return nil, nil }),
	)
	require.Empty(t, errs)
	assert.True(t, state.HasOpenAPI)
	assert.Equal(t, `{"cached":true}`, state.OpenAPIPayload)
}

func TestAssembleState_WithLiveOpenAPIProbe_LiveWorksEvenWithoutCacheProbe(t *testing.T) {
	t.Parallel()

	// The live probe must not require WithOpenAPIProbe to be set —
	// `run` may surface a payload from the freshly-started agent
	// even when no prior `invoke` has populated the on-disk cache.
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".azure", "dev"), 0o750))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
		},
	}

	fresh := []byte(`{"paths":{"/invocations":{"post":{"requestBody":{"content":{"application/json":{"example":{"live":true}}}}}}}}`)
	state, errs := assembleState(
		context.Background(),
		src,
		WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) { return fresh, nil }),
	)
	require.Empty(t, errs)
	assert.True(t, state.HasOpenAPI)
	assert.Equal(t, `{"live":true}`, state.OpenAPIPayload)
}

func TestAssembleState_WithLiveOpenAPIProbe_LiveFailureWithoutCacheLeavesUnset(t *testing.T) {
	t.Parallel()

	// Live probe errors AND no cache present → resolver must fall
	// back to the protocol-generic literal (HasOpenAPI=false).
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".azure", "dev"), 0o750))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path:     projectRoot,
			Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo", Host: agentHost}},
		},
	}

	state, errs := assembleState(
		context.Background(),
		src,
		WithOpenAPIProbe("echo", "local"),
		WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) {
			return nil, errors.New("dial tcp: connection refused")
		}),
	)
	require.Empty(t, errs)
	assert.False(t, state.HasOpenAPI)
	assert.Empty(t, state.OpenAPIPayload)
}

func TestLoadServiceProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		manifest    string // raw agent.yaml content; empty string means "do not write the file"
		manifestRel string // override relativePath in the call (for missing-dir cases)
		want        string
	}{
		{
			name: "single responses protocol",
			manifest: `kind: hostedAgent
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			want: ProtocolResponses,
		},
		{
			name: "single invocations protocol",
			manifest: `kind: hostedAgent
protocols:
  - protocol: invocations
    version: "1.0.0"
`,
			want: ProtocolInvocations,
		},
		{
			name: "single invocations_ws protocol",
			manifest: `kind: hostedAgent
protocols:
  - protocol: invocations_ws
    version: "2.0.0"
`,
			want: ProtocolInvocationsWS,
		},
		{
			name: "single activity protocol",
			manifest: `kind: hostedAgent
protocols:
  - protocol: activity
    version: "2.0.0"
`,
			want: ProtocolActivity,
		},
		{
			name: "single legacy activity protocol",
			manifest: `kind: hostedAgent
protocols:
  - protocol: activity_protocol
    version: "1.0.0"
`,
			want: ProtocolActivity,
		},
		{
			name: "responses wins when both declared",
			manifest: `kind: hostedAgent
protocols:
  - protocol: invocations
    version: "1.0.0"
  - protocol: responses
    version: "1.0.0"
`,
			want: ProtocolResponses,
		},
		{
			name: "responses wins over invocations_ws regardless of order",
			manifest: `kind: hostedAgent
protocols:
  - protocol: invocations_ws
    version: "2.0.0"
  - protocol: responses
    version: "2.0.0"
`,
			want: ProtocolResponses,
		},
		{
			name: "invocations wins over invocations_ws regardless of order",
			manifest: `kind: hostedAgent
protocols:
  - protocol: invocations_ws
    version: "2.0.0"
  - protocol: invocations
    version: "1.0.0"
`,
			want: ProtocolInvocations,
		},
		{
			name: "invocations wins over activity regardless of order",
			manifest: `kind: hostedAgent
protocols:
  - protocol: activity
    version: "2.0.0"
  - protocol: invocations
    version: "1.0.0"
`,
			want: ProtocolInvocations,
		},
		{
			name: "empty protocols section",
			manifest: `kind: hostedAgent
protocols: []
`,
			want: "",
		},
		{
			name: "unknown protocol value silently ignored",
			manifest: `kind: hostedAgent
protocols:
  - protocol: pigeon-mail
    version: "1.0.0"
`,
			want: "",
		},
		{
			name:     "malformed yaml returns empty",
			manifest: "this: is: not: valid: yaml: at: all: [",
			want:     "",
		},
		{
			name:        "missing file returns empty",
			manifestRel: "does-not-exist",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projectRoot := t.TempDir()
			relPath := "echo"
			if tt.manifestRel != "" {
				relPath = tt.manifestRel
			} else {
				svcDir := filepath.Join(projectRoot, relPath)
				require.NoError(t, os.MkdirAll(svcDir, 0o750))
				require.NoError(t, os.WriteFile(
					filepath.Join(svcDir, "agent.yaml"),
					[]byte(tt.manifest),
					0o600,
				))
			}
			got := loadServiceProtocol(projectRoot, &azdext.ServiceConfig{RelativePath: relPath})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadServiceProtocol_EmptyArgs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", loadServiceProtocol("", &azdext.ServiceConfig{RelativePath: "echo"}))
}

func TestLoadServiceProtocol_RootRelativePath(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "agent.yaml"),
		[]byte("kind: hostedAgent\nprotocols:\n  - protocol: invocations\n    version: \"1.0.0\"\n"),
		0o600,
	))

	assert.Equal(t, ProtocolInvocations, loadServiceProtocol(projectRoot, &azdext.ServiceConfig{}))
	assert.Equal(t, ProtocolInvocations, loadServiceProtocol(projectRoot, &azdext.ServiceConfig{RelativePath: "."}))
}

func TestLoadServiceProtocol_RejectsTraversal(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(projectRoot, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(outside, "agent.yaml"),
		[]byte("kind: hostedAgent\nprotocols:\n  - protocol: invocations\n    version: \"1.0.0\"\n"),
		0o600,
	))

	assert.Equal(t, "", loadServiceProtocol(projectRoot, &azdext.ServiceConfig{RelativePath: "../outside"}))
}

func TestLoadServiceProtocol_InlineAdditionalPropertiesWinOverAgentYaml(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	relPath := "echo"
	svcDir := filepath.Join(projectRoot, relPath)
	require.NoError(t, os.MkdirAll(svcDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(svcDir, "agent.yaml"),
		[]byte("kind: hostedAgent\nprotocols:\n  - protocol: responses\n    version: \"2.0.0\"\n"),
		0o600,
	))

	config, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"protocols": []any{
			map[string]any{"protocol": "invocations_ws", "version": "2.0.0"},
		},
	})
	require.NoError(t, err)

	got := loadServiceProtocol(projectRoot, &azdext.ServiceConfig{
		RelativePath:         relPath,
		AdditionalProperties: config,
	})
	assert.Equal(t, ProtocolInvocationsWS, got)
}

func TestLoadServiceProtocol_LegacyConfigFallback(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"protocols": []any{
			map[string]any{"protocol": "invocations_ws", "version": "2.0.0"},
		},
	})
	require.NoError(t, err)

	got := loadServiceProtocol(t.TempDir(), &azdext.ServiceConfig{Config: config})
	assert.Equal(t, ProtocolInvocationsWS, got)
}

func TestAssembleState_PopulatesProtocolFromAgentYaml(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte("kind: hostedAgent\nprotocols:\n  - protocol: invocations\n    version: \"1.0.0\"\n"),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, ProtocolInvocations, state.Services[0].Protocol)
}

func TestAssembleState_PopulatesProtocolFromInlineAdditionalProperties(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"protocols": []any{
			map[string]any{"protocol": "invocations_ws", "version": "2.0.0"},
		},
	})
	require.NoError(t, err)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, AdditionalProperties: config},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, ProtocolInvocationsWS, state.Services[0].Protocol)
}

func TestAssembleState_MarksInlineMultiProtocolService(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"protocols": []any{
			map[string]any{"protocol": "invocations_ws", "version": "2.0.0"},
			map[string]any{"protocol": "responses", "version": "2.0.0"},
		},
	})
	require.NoError(t, err)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: t.TempDir(),
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, AdditionalProperties: config},
			},
		},
	}

	state, errs := assembleState(t.Context(), src)
	require.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, ProtocolResponses, state.Services[0].Protocol)
	assert.True(t, state.Services[0].MultiProtocol)
}

func TestExtractEnvironmentRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		manifest         string
		wantRefs         []string
		wantPlaceholders []string
	}{
		{
			name: "single bare reference",
			manifest: `kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
`,
			wantRefs: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "reference with default tail is skipped",
			manifest: `kind: hostedAgent
environment_variables:
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME:-gpt-4o-mini}
`,
			wantRefs: nil,
		},
		{
			name: "escaped literal keeps the ref out of missing-var hints",
			manifest: `kind: hostedAgent
environment_variables:
  - name: LITERAL
    value: $${FOUNDRY_PROJECT_ENDPOINT}
`,
			wantRefs: nil,
		},
		{
			name: "bare ref alongside defaulted ref returns only the bare one",
			manifest: `kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME:-gpt-4o-mini}
`,
			wantRefs: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "multiple references in one value",
			manifest: `kind: hostedAgent
environment_variables:
  - name: CONN
    value: postgresql://${DB_HOST}:5432/${DB_NAME}
`,
			wantRefs: []string{"DB_HOST", "DB_NAME"},
		},
		{
			name: "duplicate references deduplicated by first appearance",
			manifest: `kind: hostedAgent
environment_variables:
  - name: A
    value: ${X}-${X}
  - name: B
    value: ${X}
`,
			wantRefs: []string{"X"},
		},
		{
			name: "no environment_variables block",
			manifest: `kind: hostedAgent
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			wantRefs: nil,
		},
		{
			name: "literal value with no ${} reference",
			manifest: `kind: hostedAgent
environment_variables:
  - name: STATIC
    value: hardcoded
`,
			wantRefs: nil,
		},
		{
			name:     "malformed yaml returns nil",
			manifest: "this: is: not: valid: yaml: at: all: [",
			wantRefs: nil,
		},
		{
			name: "mustache placeholder surfaced separately",
			manifest: `kind: hostedAgent
environment_variables:
  - name: TOOLBOX_ENDPOINT
    value: '{{TOOLBOX_ENDPOINT}}'
`,
			wantPlaceholders: []string{"TOOLBOX_ENDPOINT"},
		},
		{
			name: "mustache placeholder with internal whitespace",
			manifest: `kind: hostedAgent
environment_variables:
  - name: KEY
    value: '{{ MY_KEY }}'
`,
			wantPlaceholders: []string{"MY_KEY"},
		},
		{
			name: "duplicate placeholders deduplicated",
			manifest: `kind: hostedAgent
environment_variables:
  - name: A
    value: '{{X}}-{{X}}'
  - name: B
    value: '{{X}}'
`,
			wantPlaceholders: []string{"X"},
		},
		{
			name: "ref and placeholder coexist in same manifest",
			manifest: `kind: hostedAgent
environment_variables:
  - name: TOOLBOX_ENDPOINT
    value: '{{TOOLBOX_ENDPOINT}}'
  - name: MCP_ENDPOINT
    value: ${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}
`,
			wantRefs:         []string{"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"},
			wantPlaceholders: []string{"TOOLBOX_ENDPOINT"},
		},
		{
			// Manifest parameter names are not constrained to env-var
			// identifier shape — parameters.go:injectParameterValues
			// substitutes the raw YAML key without validating it.
			// A surviving `{{toolbox-endpoint}}` (hyphen) must therefore
			// still be flagged or the user gets no Next: hint.
			name: "mustache placeholder with hyphen in name",
			manifest: `kind: hostedAgent
environment_variables:
  - name: TOOLBOX_ENDPOINT
    value: '{{toolbox-endpoint}}'
`,
			wantPlaceholders: []string{"toolbox-endpoint"},
		},
		{
			name: "mustache placeholder with dot in name",
			manifest: `kind: hostedAgent
environment_variables:
  - name: COMPONENT
    value: '{{my.component.id}}'
`,
			wantPlaceholders: []string{"my.component.id"},
		},
		{
			name: "Foundry expressions are not placeholders",
			manifest: `kind: hostedAgent
 environment_variables:
   - name: PROJECT_ENDPOINT
     value: '${{project.endpoint}}'
   - name: PROJECT_NAME
     value: '$${{project.name}}'
 `,
		},
		{
			// Empty placeholder body must not be flagged — it cannot
			// correspond to a manifest parameter and is more likely
			// stray literal text.
			name: "empty mustache braces are ignored",
			manifest: `kind: hostedAgent
environment_variables:
  - name: NOISE
    value: 'preamble {{}} suffix'
`,
			wantPlaceholders: nil,
		},
		{
			// Whitespace-only placeholder body is similarly garbage —
			// must not be flagged.
			name: "whitespace-only mustache braces are ignored",
			manifest: `kind: hostedAgent
environment_variables:
  - name: NOISE
    value: 'preamble {{   }} suffix'
`,
			wantPlaceholders: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotRefs, gotPlaceholders := extractEnvironmentRefs(
				environmentValuesFromManifest(t, tt.manifest),
			)
			assert.Equal(t, tt.wantRefs, gotRefs, "refs")
			assert.Equal(t, tt.wantPlaceholders, gotPlaceholders, "placeholders")
		})
	}
}

func environmentValuesFromManifest(t *testing.T, data string) []string {
	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal([]byte(data), &hosted); err != nil {
		return nil
	}
	if hosted.EnvironmentVariables == nil {
		return nil
	}
	values := make([]string, 0, len(*hosted.EnvironmentVariables))
	for _, value := range *hosted.EnvironmentVariables {
		values = append(values, value.Value)
	}
	return values
}

func TestExtractEnvironmentRefs_EmptyValues(t *testing.T) {
	t.Parallel()

	refs, placeholders := extractEnvironmentRefs(nil)
	assert.Nil(t, refs)
	assert.Nil(t, placeholders)
}

func TestAssembleState_PopulatesMissingVars(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
  - name: KEY
    value: ${MY_API_KEY}
  - name: STATIC
    value: hardcoded
`),
		0o600,
	))
	// infra/main.bicep declares both AZURE_* names as outputs, so they
	// route to MissingInfraVars when unset. MY_API_KEY has no Bicep
	// output and routes to MissingManualVars.
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output FOUNDRY_PROJECT_ENDPOINT string = ''
output AZURE_AI_MODEL_DEPLOYMENT_NAME string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
		values: map[string]string{
			// AZURE_AI_MODEL_DEPLOYMENT_NAME is set; the other two are not.
			"dev/AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-4o-mini",
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"FOUNDRY_PROJECT_ENDPOINT"}, state.MissingInfraVars)
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
}

func TestAssembleState_UsesUnifiedEnvironment(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAzureYAML(t, projectRoot, `
services:
  echo:
    host: azure.ai.agent
    env:
      SHARED: set
`)
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte("output INFRA_VALUE string = ''\n"),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": newAgentService(t, map[string]any{
					"kind": "hostedAgent",
					"environmentVariables": []any{
						map[string]any{
							"name":  "INFRA",
							"value": "${INFRA_VALUE}",
						},
						map[string]any{
							"name":  "MANUAL",
							"value": "${MANUAL_VALUE}",
						},
						map[string]any{
							"name":  "DEFAULT",
							"value": "${DEFAULT_VALUE:-fallback}",
						},
						map[string]any{
							"name":  "FOUNDRY",
							"value": "${{project.endpoint}}",
						},
						map[string]any{
							"name":  "PLACEHOLDER",
							"value": "{{PLACEHOLDER}}",
						},
						map[string]any{
							"name":  "SHARED",
							"value": "${SHOULD_NOT_BE_READ}",
						},
					},
				}),
			},
		},
		values: map[string]string{
			"dev/MANUAL_VALUE": "set",
		},
	}

	state, errs := assembleState(context.Background(), src)

	require.Empty(t, errs)
	assert.Equal(t, []string{"INFRA_VALUE"}, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
	assert.Equal(t, []string{"PLACEHOLDER"}, state.UnresolvedPlaceholders)
	assert.Empty(t, state.EnvironmentLoadErrors)
}

func TestAssembleState_UsesReferencedUnifiedEnvironment(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAzureYAML(t, projectRoot, `
services:
  echo:
    host: azure.ai.agent
    $ref: ./service.yaml
`)
	writeProjectFile(t, projectRoot, "service.yaml", `
kind: hostedAgent
environmentVariables:
  - name: REF_VALUE
    value: ${REF_VALUE}
`)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {
					Name:                 "echo",
					Host:                 agentHost,
					AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./service.yaml"}),
				},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	require.Empty(t, errs)
	assert.Equal(t, []string{"REF_VALUE"}, state.MissingManualVars)
	assert.Empty(t, state.MissingInfraVars)
	assert.Equal(t, []string{"${REF_VALUE}"}, state.Services[0].EnvironmentValues)
}

func TestAssembleState_EnvironmentLoadErrorIsRecorded(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAzureYAML(t, projectRoot, `
services:
  echo:
    host: azure.ai.agent
    env:
      INVALID:
        - value
`)

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": newAgentService(t, map[string]any{
					"kind": "hostedAgent",
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)

	require.NotEmpty(t, errs)
	require.NotNil(t, state)
	require.Len(t, state.EnvironmentLoadErrors, 1)
	assert.Contains(
		t,
		state.EnvironmentLoadErrors[0],
		`service "echo" agent environment`,
	)
	assert.Empty(t, state.MissingManualVars)
}

func TestAssembleState_MissingVarsDedupedAcrossServices(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	manifest := []byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: KEY
    value: ${MY_API_KEY}
`)
	for _, rel := range []string{"echo", "ping"} {
		require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, rel), 0o750))
		require.NoError(t, os.WriteFile(
			filepath.Join(projectRoot, rel, "agent.yaml"),
			manifest,
			0o600,
		))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output FOUNDRY_PROJECT_ENDPOINT string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
				"ping": {Name: "ping", Host: agentHost, RelativePath: "ping"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"FOUNDRY_PROJECT_ENDPOINT"}, state.MissingInfraVars)
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
}

func TestAssembleState_AllVarsSetLeavesMissingEmpty(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
		values: map[string]string{
			"dev/FOUNDRY_PROJECT_ENDPOINT": "https://x.services.ai.azure.com",
			"dev/MY_API_KEY":               "sk-abc",
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
}

func TestAssembleState_DefaultedRefsAreExcludedFromMissingVars(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	// Both refs use POSIX ${VAR:-default} syntax; the deploy-time expander
	// honors the default so neither variable is required. The bare-form
	// ENDPOINT ref is unset and IS required, so it still surfaces.
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME:-gpt-4o-mini}
  - name: KEY
    value: ${MY_API_KEY:-dev-fallback}
`),
		0o600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output FOUNDRY_PROJECT_ENDPOINT string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
		// Intentionally leave AZURE_AI_MODEL_DEPLOYMENT_NAME and MY_API_KEY
		// unset; defaulted refs must not surface them as missing.
		values: map[string]string{},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"FOUNDRY_PROJECT_ENDPOINT"}, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
}

func TestAssembleState_MissingVarTransportErrorSurfaced(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
		valueErr: errors.New("gRPC unavailable"),
	}

	state, errs := assembleState(context.Background(), src)
	// One error each for FOUNDRY_PROJECT_ENDPOINT, AI_AGENT_PENDING_PROVISION,
	// AZURE_SUBSCRIPTION_ID, AZURE_LOCATION, AGENT_ECHO_VERSION, and MY_API_KEY.
	assert.Len(t, errs, 6)
	assert.Empty(t, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
}

func TestAssembleState_PopulatesUnresolvedPlaceholders(t *testing.T) {
	t.Parallel()

	// Reproduces the toolbox-sample bug: agent.manifest.yaml processing
	// leaves a {{NAME}} placeholder behind in agent.yaml, while a separate
	// env var ref is also unset. The resolver should see both.
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: TOOLBOX_ENDPOINT
    value: '{{TOOLBOX_ENDPOINT}}'
  - name: MCP_ENDPOINT
    value: ${TOOLBOX_MCP_ENDPOINT}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Equal(t, []string{"TOOLBOX_MCP_ENDPOINT"}, state.MissingManualVars)
	assert.Equal(t, []string{"TOOLBOX_ENDPOINT"}, state.UnresolvedPlaceholders)
}

// TestAssembleState_PartitionsToolboxEndpointVars locks the partition
// behavior added for the toolbox-sample post-init UX: when a service
// has a manifest-declared toolbox AND agent.yaml references the
// canonical TOOLBOX_<NAME>_MCP_ENDPOINT env var, the missing-var
// classifier must route the entry into MissingToolboxEndpoints
// (provision-managed) rather than MissingManualVars (operator-supplied).
// Non-toolbox manual vars in the same agent.yaml must still appear in
// MissingManualVars.
func TestAssembleState_PartitionsToolboxEndpointVars(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	// agent.manifest.yaml declares the toolbox by name; envkey derives
	// "TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT" from "web-search-tools",
	// matching the ${...} ref in agent.yaml below.
	writeManifest(t, projectRoot, "echo", `
template:
  kind: containerAgent
  name: hello
resources:
  - name: web-search-tools
    kind: toolbox
    tools:
      - id: tool-1
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: MCP_ENDPOINT
    value: ${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}
  - name: API_KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	// Toolbox endpoint var moved into the dedicated bucket; the
	// generic manual-var bucket only carries the truly user-supplied
	// API key.
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
	require.Len(t, state.MissingToolboxEndpoints, 1)
	assert.Equal(t, "web-search-tools", state.MissingToolboxEndpoints[0].Name)
	assert.Equal(t, "echo", state.MissingToolboxEndpoints[0].ServiceName)
}

// TestAssembleState_ToolboxEndpointWithoutManifestStaysManual locks
// the partition's guard: a TOOLBOX_*_MCP_ENDPOINT-shaped variable
// whose name does NOT match a manifest-declared toolbox is treated
// as a generic user variable and stays in MissingManualVars. The
// partition is a no-op when no manifest toolbox claims the key.
func TestAssembleState_ToolboxEndpointWithoutManifestStaysManual(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: MCP_ENDPOINT
    value: ${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"}, state.MissingManualVars)
	assert.Empty(t, state.MissingToolboxEndpoints)
}

func TestAssembleState_PlaceholdersDedupedAcrossServices(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	manifest := []byte(`kind: hostedAgent
environment_variables:
  - name: A
    value: '{{SHARED_PLACEHOLDER}}'
`)
	for _, rel := range []string{"echo", "ping"} {
		require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, rel), 0o750))
		require.NoError(t, os.WriteFile(
			filepath.Join(projectRoot, rel, "agent.yaml"),
			manifest,
			0o600,
		))
	}

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
				"ping": {Name: "ping", Host: agentHost, RelativePath: "ping"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"SHARED_PLACEHOLDER"}, state.UnresolvedPlaceholders)
}

// TestAssembleState_NonAzurePrefixBicepOutputIsInfra is the B1 fix proof.
// It locks issue #7975 State Inputs line 74 ("HasUnresolvedInfraVars =
// agent.yaml ${VAR} refs that map to known Bicep outputs are unset in
// azd env"). Pre-C1, the resolver split on the AZURE_ prefix; this
// test guarantees the new classifier is set-membership based and
// correctly routes a non-AZURE_ Bicep output to MissingInfraVars.
func TestAssembleState_NonAzurePrefixBicepOutputIsInfra(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: TOOLBOX
    value: ${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}
  - name: BING
    value: ${BING_GROUNDING_CONNECTION_ID}
  - name: KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT string = ''
output BING_GROUNDING_CONNECTION_ID string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(
		t,
		[]string{"BING_GROUNDING_CONNECTION_ID", "TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"},
		state.MissingInfraVars,
	)
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
}

// TestAssembleState_NoBicepFileEverythingManual locks the conservative
// fallback: when infra/main.bicep is missing, every unset ref lands in
// MissingManualVars. Notably this includes AZURE_*-prefixed names —
// without the prefix shortcut, AZURE_ has no special meaning anymore.
func TestAssembleState_NoBicepFileEverythingManual(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}
  - name: TOOLBOX
    value: ${TOOLBOX_MCP_ENDPOINT}
  - name: KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Equal(
		t,
		[]string{"FOUNDRY_PROJECT_ENDPOINT", "MY_API_KEY", "TOOLBOX_MCP_ENDPOINT"},
		state.MissingManualVars,
	)
}

// TestAssembleState_DeclaredAndSetBicepOutputNotSurfaced locks the
// sanity case: a ref that maps to a Bicep output AND is set in the
// current env is not missing from either bucket.
func TestAssembleState_DeclaredAndSetBicepOutputNotSurfaced(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: TOOLBOX
    value: ${TOOLBOX_MCP_ENDPOINT}
`),
		0o600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output TOOLBOX_MCP_ENDPOINT string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
		values: map[string]string{
			"dev/TOOLBOX_MCP_ENDPOINT": "https://mcp.example/x",
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
}

// TestAssembleState_UndeclaredRefIsManualEvenWithBicepFile locks the
// other half of set-membership classification: when infra/main.bicep
// exists but does NOT declare a ref'd var, the var lands in
// MissingManualVars (not MissingInfraVars).
func TestAssembleState_UndeclaredRefIsManualEvenWithBicepFile(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "echo"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "echo", "agent.yaml"),
		[]byte(`kind: hostedAgent
environment_variables:
  - name: KEY
    value: ${MY_API_KEY}
`),
		0o600,
	))
	// Bicep file exists but doesn't declare MY_API_KEY → manual.
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(`output FOUNDRY_PROJECT_ENDPOINT string = ''
`),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo": {Name: "echo", Host: agentHost, RelativePath: "echo"},
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
}
