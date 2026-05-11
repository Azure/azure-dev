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
				values:  map[string]string{"dev/AZURE_AI_PROJECT_ENDPOINT": "https://x.services.ai.azure.com"},
				project: &azdext.ProjectConfig{Name: "demo"},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.True(t, state.HasProjectEndpoint)
				assert.Empty(t, state.Services)
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
			},
			// One error for AZURE_AI_PROJECT_ENDPOINT + one per service lookup = 2.
			errCount: 2,
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

func TestOptionsApplyCleanly(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithAuthProbe(true)(cfg)
	WithOpenAPIProbe("echo", "local")(cfg)
	assert.True(t, cfg.authProbe)
	assert.Equal(t, "echo", cfg.openAPIAgent)
	assert.Equal(t, "local", cfg.openAPISuffix)
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
		tt := tt
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
			got := loadServiceProtocol(projectRoot, relPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadServiceProtocol_EmptyArgs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", loadServiceProtocol("", "echo"))
	assert.Equal(t, "", loadServiceProtocol("/some/path", ""))
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

func TestExtractAgentYamlEnvRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
		want     []string
	}{
		{
			name: "single bare reference",
			manifest: `kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${AZURE_AI_PROJECT_ENDPOINT}
`,
			want: []string{"AZURE_AI_PROJECT_ENDPOINT"},
		},
		{
			name: "reference with default tail captured as bare name",
			manifest: `kind: hostedAgent
environment_variables:
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME:-gpt-4o-mini}
`,
			want: []string{"AZURE_AI_MODEL_DEPLOYMENT_NAME"},
		},
		{
			name: "multiple references in one value",
			manifest: `kind: hostedAgent
environment_variables:
  - name: CONN
    value: postgresql://${DB_HOST}:5432/${DB_NAME}
`,
			want: []string{"DB_HOST", "DB_NAME"},
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
			want: []string{"X"},
		},
		{
			name: "no environment_variables block",
			manifest: `kind: hostedAgent
protocols:
  - protocol: responses
    version: "1.0.0"
`,
			want: nil,
		},
		{
			name: "literal value with no ${} reference",
			manifest: `kind: hostedAgent
environment_variables:
  - name: STATIC
    value: hardcoded
`,
			want: nil,
		},
		{
			name:     "malformed yaml returns nil",
			manifest: "this: is: not: valid: yaml: at: all: [",
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projectRoot := t.TempDir()
			svcDir := filepath.Join(projectRoot, "echo")
			require.NoError(t, os.MkdirAll(svcDir, 0o750))
			require.NoError(t, os.WriteFile(
				filepath.Join(svcDir, "agent.yaml"),
				[]byte(tt.manifest),
				0o600,
			))
			got := extractAgentYamlEnvRefs(projectRoot, "echo")
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractAgentYamlEnvRefs_MissingFileOrArgs(t *testing.T) {
	t.Parallel()

	assert.Nil(t, extractAgentYamlEnvRefs("", "echo"))
	assert.Nil(t, extractAgentYamlEnvRefs(t.TempDir(), ""))
	assert.Nil(t, extractAgentYamlEnvRefs(t.TempDir(), "missing"))
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
    value: ${AZURE_AI_PROJECT_ENDPOINT}
  - name: MODEL
    value: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
  - name: KEY
    value: ${MY_API_KEY}
  - name: STATIC
    value: hardcoded
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
	assert.Equal(t, []string{"AZURE_AI_PROJECT_ENDPOINT"}, state.MissingInfraVars)
	assert.Equal(t, []string{"MY_API_KEY"}, state.MissingManualVars)
}

func TestAssembleState_MissingVarsDedupedAcrossServices(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	manifest := []byte(`kind: hostedAgent
environment_variables:
  - name: ENDPOINT
    value: ${AZURE_AI_PROJECT_ENDPOINT}
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
	assert.Equal(t, []string{"AZURE_AI_PROJECT_ENDPOINT"}, state.MissingInfraVars)
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
    value: ${AZURE_AI_PROJECT_ENDPOINT}
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
			"dev/AZURE_AI_PROJECT_ENDPOINT": "https://x.services.ai.azure.com",
			"dev/MY_API_KEY":                "sk-abc",
		},
	}

	state, errs := assembleState(context.Background(), src)
	require.Empty(t, errs)
	assert.Empty(t, state.MissingInfraVars)
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
	// One error for AZURE_AI_PROJECT_ENDPOINT + AGENT_ECHO_VERSION + MY_API_KEY.
	assert.Len(t, errs, 3)
	assert.Empty(t, state.MissingInfraVars)
	assert.Empty(t, state.MissingManualVars)
}
