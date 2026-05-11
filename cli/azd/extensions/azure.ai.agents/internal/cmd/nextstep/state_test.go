// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
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
						"echo": {Name: "echo", Host: "agent", RelativePath: "./src/echo"},
					},
				},
				values: map[string]string{},
			},
			assert: func(t *testing.T, state *State, _ []error) {
				assert.False(t, state.HasProjectEndpoint)
				require.Len(t, state.Services, 1)
				assert.Equal(t, "echo", state.Services[0].Name)
				assert.Equal(t, "agent", state.Services[0].Host)
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
						"chat-bot":   {Name: "chat-bot", Host: "agent"},
						"echo":       {Name: "echo", Host: "agent"},
						"my service": {Name: "my service", Host: "agent"},
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
			name: "transport error on env-value read does not abort assembly",
			src: &fakeSource{
				envName:  "dev",
				project:  &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{"echo": {Name: "echo"}}},
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
				"good": {Name: "good"},
				"nil":  nil,
			},
		},
	}

	state, errs := assembleState(context.Background(), src)
	assert.Empty(t, errs)
	require.Len(t, state.Services, 1)
	assert.Equal(t, "good", state.Services[0].Name)
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
	WithOpenAPIProbe(true)(cfg)
	assert.True(t, cfg.authProbe)
	assert.True(t, cfg.openAPIProbe)
}
