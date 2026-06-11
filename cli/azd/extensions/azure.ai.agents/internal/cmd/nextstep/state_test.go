// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeSource is a hand-rolled Source for table-driven tests.
type fakeSource struct {
	envName    string
	envNameErr error
	project    *azdext.ProjectConfig
	projectErr error
	values     map[string]string
	valueErr   error
}

func (f *fakeSource) CurrentEnvName(_ context.Context) (string, error) {
	return f.envName, f.envNameErr
}

func (f *fakeSource) Project(_ context.Context) (*azdext.ProjectConfig, error) {
	return f.project, f.projectErr
}

func (f *fakeSource) EnvValue(_ context.Context, envName, key string) (string, error) {
	if f.valueErr != nil {
		return "", f.valueErr
	}
	return f.values[envName+"/"+key], nil
}

// foundrySvc builds a `host: microsoft.foundry` ServiceConfig whose
// AdditionalProperties carry the given top-level Foundry keys (deployments,
// connections, toolboxes, skills, routines, agents, endpoint). This mirrors
// how core azd forwards the unified service entry over gRPC.
func foundrySvc(t *testing.T, name string, props map[string]any) *azdext.ServiceConfig {
	t.Helper()
	s, err := structpb.NewStruct(props)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: name, Host: agentHost, AdditionalProperties: s}
}

// oneAgentSvc builds a Foundry service named `name` containing a single
// hosted agent named `agentName`, with the given project dir (omitted when
// empty) and env map (omitted when nil).
func oneAgentSvc(t *testing.T, name, agentName, project string, env map[string]any) *azdext.ServiceConfig {
	t.Helper()
	agent := map[string]any{"name": agentName, "kind": agentKindHosted}
	if project != "" {
		agent["project"] = project
	}
	if env != nil {
		agent["env"] = env
	}
	return foundrySvc(t, name, map[string]any{"agents": []any{agent}})
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
			name: "endpoint env var set, no services: HasProjectEndpoint true",
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
			name: "explicit endpoint field on service marks HasProjectEndpoint",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"proj": foundrySvc(t, "proj", map[string]any{
							"endpoint": "https://acct.services.ai.azure.com/api/projects/p",
							"agents":   []any{map[string]any{"name": "a1", "kind": "prompt"}},
						}),
					},
				},
				values: map[string]string{},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.True(t, state.HasProjectEndpoint, "endpoint: field should mark the project as present")
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
			name: "endpoint unset, one undeployed agent",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"proj": oneAgentSvc(t, "proj", "echo", "./src/echo", nil),
					},
				},
				values: map[string]string{},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.False(t, state.HasProjectEndpoint)
				require.Len(t, state.Services, 1)
				assert.Equal(t, "echo", state.Services[0].Name)
				assert.Equal(t, agentKindHosted, state.Services[0].Kind)
				assert.Equal(t, agentHost, state.Services[0].Host)
				assert.Equal(t, "proj", state.Services[0].ServiceName)
				assert.Equal(t, "./src/echo", state.Services[0].RelativePath)
				assert.False(t, state.Services[0].IsDeployed)
			},
		},
		{
			name: "multiple agents: deployed flag follows AGENT_<KEY>_VERSION, alphabetical order",
			src: &fakeSource{
				envName: "prod",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"proj": foundrySvc(t, "proj", map[string]any{
							"agents": []any{
								map[string]any{"name": "chat-bot", "kind": "hosted"},
								map[string]any{"name": "echo", "kind": "hosted"},
								map[string]any{"name": "my service", "kind": "hosted"},
							},
						}),
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
			name: "non-foundry services are filtered out",
			src: &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Services: map[string]*azdext.ServiceConfig{
						"proj":   oneAgentSvc(t, "proj", "echo", "", nil),
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
					Services: map[string]*azdext.ServiceConfig{
						"proj": oneAgentSvc(t, "proj", "echo", "", nil),
					},
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
			// AZURE_LOCATION + one per agent lookup
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

func TestAssembleState_NilServiceEntriesAreIgnored(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"good": oneAgentSvc(t, "good", "good-agent", "", nil),
				"nil":  nil,
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	assert.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, "good-agent", state.Services[0].Name)
}

func TestAgentHostConstant(t *testing.T) {
	t.Parallel()
	// agentHost must match the unified-design host kind. Pinning the
	// literal guards against accidental drift while the duplication with
	// the doctor package and cmd.AiAgentHost exists.
	assert.Equal(t, "microsoft.foundry", agentHost)
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

func TestOptionsApplyCleanly(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithOpenAPIProbe("echo", "local")(cfg)
	WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) { return nil, nil })(cfg)
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

func TestPreferredProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		protocols []foundryProtocol
		want      string
	}{
		{"single responses", []foundryProtocol{{Protocol: "responses"}}, ProtocolResponses},
		{"single invocations", []foundryProtocol{{Protocol: "invocations"}}, ProtocolInvocations},
		{
			"responses wins when both declared",
			[]foundryProtocol{{Protocol: "invocations"}, {Protocol: "responses"}},
			ProtocolResponses,
		},
		{"empty list", nil, ""},
		{"unknown value ignored", []foundryProtocol{{Protocol: "pigeon-mail"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, preferredProtocol(tt.protocols))
		})
	}
}

func TestAssembleState_PopulatesProtocolFromAgents(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"proj": foundrySvc(t, "proj", map[string]any{
					"agents": []any{
						map[string]any{
							"name": "echo",
							"kind": "hosted",
							"protocols": []any{
								map[string]any{"protocol": "invocations", "version": "1.0.0"},
								map[string]any{"protocol": "responses", "version": "1.0.0"},
							},
						},
					},
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, ProtocolResponses, state.Services[0].Protocol)
}

func TestExtractEnvRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"nil env", nil, nil},
		{"no refs", map[string]string{"A": "static"}, nil},
		{"single bare ref", map[string]string{"A": "${MY_KEY}"}, []string{"MY_KEY"}},
		{"defaulted ref skipped", map[string]string{"A": "${MY_KEY:-fallback}"}, nil},
		{
			"foundry server-side ref ignored",
			map[string]string{"A": "${{connections.x.credentials.key}}"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.ElementsMatch(t, tt.want, extractEnvRefs(tt.env))
		})
	}
}

func TestAssembleState_PopulatesMissingVars(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
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
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}",
					"MODEL":    "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
					"KEY":      "${MY_API_KEY}",
					"STATIC":   "hardcoded",
				}),
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

func TestAssembleState_MissingVarsDedupedAcrossAgents(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte("output FOUNDRY_PROJECT_ENDPOINT string = ''\n"),
		0o600,
	))

	env := map[string]any{
		"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}",
		"KEY":      "${MY_API_KEY}",
	}
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": foundrySvc(t, "proj", map[string]any{
					"agents": []any{
						map[string]any{"name": "echo", "kind": "hosted", "env": env},
						map[string]any{"name": "ping", "kind": "hosted", "env": env},
					},
				}),
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
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}",
					"KEY":      "${MY_API_KEY}",
				}),
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
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte("output FOUNDRY_PROJECT_ENDPOINT string = ''\n"),
		0o600,
	))

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				// Both MODEL and KEY use ${VAR:-default}; the deploy-time
				// expander honors the default, so neither is required. The
				// bare-form ENDPOINT ref is unset and IS required.
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}",
					"MODEL":    "${AZURE_AI_MODEL_DEPLOYMENT_NAME:-gpt-4o-mini}",
					"KEY":      "${MY_API_KEY:-dev-fallback}",
				}),
			},
		},
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
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"KEY": "${MY_API_KEY}",
				}),
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

// TestAssembleState_PartitionsToolboxEndpointVars locks the partition
// behavior: when a Foundry service declares a toolbox AND an agent's env
// references the canonical TOOLBOX_<NAME>_MCP_ENDPOINT env var, the
// missing-var classifier routes the entry into MissingToolboxEndpoints
// (provision-managed) rather than MissingManualVars (operator-supplied).
// Non-toolbox manual vars must still appear in MissingManualVars.
func TestAssembleState_PartitionsToolboxEndpointVars(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	// The toolbox "web-search-tools" derives the canonical env var
	// "TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT", matching the ${...} ref in
	// the agent env below.
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": foundrySvc(t, "proj", map[string]any{
					"toolboxes": []any{map[string]any{"name": "web-search-tools"}},
					"agents": []any{
						map[string]any{"name": "echo", "kind": "hosted", "env": map[string]any{
							"MCP_ENDPOINT": "${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}",
							"API_KEY":      "${MY_API_KEY}",
						}},
					},
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	// Toolbox endpoint var moved into the dedicated bucket; the generic
	// manual-var bucket only carries the truly user-supplied API key.
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
	require.Len(t, state.MissingToolboxEndpoints, 1)
	assert.Equal(t, "web-search-tools", state.MissingToolboxEndpoints[0].Name)
	assert.Equal(t, "proj", state.MissingToolboxEndpoints[0].ServiceName)
}

// TestAssembleState_ToolboxEndpointWithoutDeclarationStaysManual locks the
// partition's guard: a TOOLBOX_*_MCP_ENDPOINT-shaped variable whose name
// does NOT match a declared toolbox is treated as a generic user variable
// and stays in MissingManualVars.
func TestAssembleState_ToolboxEndpointWithoutDeclarationStaysManual(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"MCP_ENDPOINT": "${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}",
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Equal(t, []string{"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"}, state.MissingManualVars)
	assert.Empty(t, state.MissingToolboxEndpoints)
}

func TestAssembleState_NonAzurePrefixBicepOutputIsInfra(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
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
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"TOOLBOX": "${TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT}",
					"BING":    "${BING_GROUNDING_CONNECTION_ID}",
					"KEY":     "${MY_API_KEY}",
				}),
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

func TestAssembleState_NoBicepFileEverythingManual(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"proj": oneAgentSvc(t, "proj", "echo", "", map[string]any{
					"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}",
					"KEY":      "${MY_API_KEY}",
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
	assert.Equal(t, []string{"FOUNDRY_PROJECT_ENDPOINT", "MY_API_KEY"}, state.MissingManualVars)
}

// TestAssembleState_AggregatesResources verifies collectFoundry surfaces
// model deployments, toolboxes, and connections (with detail strings) and
// the matching Has* flags from the unified service entry.
func TestAssembleState_AggregatesResources(t *testing.T) {
	t.Parallel()

	src := &fakeSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"proj": foundrySvc(t, "proj", map[string]any{
					"deployments": []any{
						map[string]any{
							"name":  "gpt-4.1-mini",
							"model": map[string]any{"format": "OpenAI", "name": "gpt-4.1-mini"},
						},
					},
					"toolboxes": []any{map[string]any{"name": "support-toolbox"}},
					"connections": []any{
						map[string]any{"name": "search-conn", "category": "CognitiveSearch", "target": "https://x"},
					},
					"agents": []any{map[string]any{"name": "a1", "kind": "prompt"}},
				}),
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)

	require.True(t, state.HasModels)
	require.Len(t, state.ModelRefs, 1)
	assert.Equal(t, "gpt-4.1-mini", state.ModelRefs[0].Name)
	assert.Equal(t, "OpenAI/gpt-4.1-mini", state.ModelRefs[0].Detail)
	assert.Equal(t, "proj", state.ModelRefs[0].ServiceName)

	require.True(t, state.HasToolboxes)
	require.Len(t, state.Toolboxes, 1)
	assert.Equal(t, "support-toolbox", state.Toolboxes[0].Name)

	require.True(t, state.HasConnections)
	require.Len(t, state.Connections, 1)
	assert.Equal(t, "search-conn", state.Connections[0].Name)
	assert.Equal(t, "CognitiveSearch | https://x", state.Connections[0].Detail)
}
