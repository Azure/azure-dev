// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestApplyVnextMetadata(t *testing.T) {
	tests := []struct {
		name          string
		azdEnv        map[string]string
		osEnvValue    string
		existingMeta  map[string]string
		expectEnabled bool
	}{
		{
			name:          "enabled via azd env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			expectEnabled: true,
		},
		{
			name:          "enabled via azd env value 1",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "1"},
			expectEnabled: true,
		},
		{
			name:          "disabled via azd env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "false"},
			expectEnabled: false,
		},
		{
			name:          "enabled via OS env fallback",
			azdEnv:        map[string]string{},
			osEnvValue:    "true",
			expectEnabled: true,
		},
		{
			name:          "azd env takes precedence over OS env",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "false"},
			osEnvValue:    "true",
			expectEnabled: false,
		},
		{
			name:          "absent from both envs",
			azdEnv:        map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "invalid value ignored",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "notabool"},
			expectEnabled: false,
		},
		{
			name:          "preserves existing metadata",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			existingMeta:  map[string]string{"authors": "test"},
			expectEnabled: true,
		},
		{
			name:          "nil metadata initialized when enabled",
			azdEnv:        map[string]string{"enableHostedAgentVNext": "true"},
			existingMeta:  nil,
			expectEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set/unset OS env
			if tt.osEnvValue != "" {
				t.Setenv("enableHostedAgentVNext", tt.osEnvValue)
			} else {
				t.Setenv("enableHostedAgentVNext", "")
			}

			request := &agent_api.CreateAgentRequest{
				Name: "test-agent",
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					Metadata: tt.existingMeta,
				},
			}

			applyVnextMetadata(request, tt.azdEnv)

			val, exists := request.Metadata["enableVnextExperience"]
			if tt.expectEnabled {
				if !exists || val != "true" {
					t.Errorf("expected enableVnextExperience=true in metadata, got exists=%v val=%q", exists, val)
				}
			} else {
				if exists {
					t.Errorf("expected enableVnextExperience to be absent, but found val=%q", val)
				}
			}

			// Verify existing metadata is preserved
			if tt.existingMeta != nil {
				for k, v := range tt.existingMeta {
					if request.Metadata[k] != v {
						t.Errorf("existing metadata key %q was lost or changed: want %q, got %q", k, v, request.Metadata[k])
					}
				}
			}
		})
	}
}

func TestGetServiceKey_NormalizesToolboxNames(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"hyphens", "agent-tools", "AGENT_TOOLS"},
		{"spaces", "agent tools", "AGENT_TOOLS"},
		{"mixed", "my-agent tools", "MY_AGENT_TOOLS"},
		{"already upper", "TOOLS", "TOOLS"},
		{"lowercase", "tools", "TOOLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.getServiceKey(tt.input)
			if got != tt.expected {
				t.Errorf("getServiceKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// --- helpers for Package ACR-validation tests ---

// packageEnvServer is a minimal EnvironmentServiceServer that stubs GetValue
// and GetCurrent for Package tests.
type packageEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values  map[string]string
	getErr  error
	envName string
}

func (s *packageEnvServer) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.envName},
	}, nil
}

func (s *packageEnvServer) GetValue(_ context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	v := s.values[req.Key]
	return &azdext.KeyValueResponse{Key: req.Key, Value: v}, nil
}

// newPackageTestClient spins up a gRPC server with the given env server and
// returns an AzdClient connected to it. Cleanup is handled by t.Cleanup.
func newPackageTestClient(t *testing.T, envSrv azdext.EnvironmentServiceServer) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(srv, envSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// writeHostedAgentYAML creates a minimal hosted-kind agent.yaml in dir and
// returns the full path.
func writeHostedAgentYAML(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "agent.yaml")
	content := []byte("kind: hosted\nname: test-agent\n")
	require.NoError(t, os.WriteFile(p, content, 0o600))
	return p
}

// writePromptAgentYAML creates a minimal prompt-kind agent.yaml in dir and
// returns the full path.
func writePromptAgentYAML(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "agent.yaml")
	content := []byte("kind: prompt\nname: test-agent\nmodel:\n  id: gpt-4o-mini\n")
	require.NoError(t, os.WriteFile(p, content, 0o600))
	return p
}

func TestPackage_ACREndpointValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agentKind  string // "hosted" or "prompt"
		envValues  map[string]string
		envGetErr  error
		wantErr    bool
		wantCode   string
		wantNotACR bool // when true, expect an error but NOT an ACR-related code
	}{
		{
			name:      "returns error when ACR endpoint is empty",
			agentKind: "hosted",
			envValues: map[string]string{},
			wantErr:   true,
			wantCode:  exterrors.CodeMissingContainerRegistryEndpoint,
		},
		{
			name:      "returns error when GetValue fails",
			agentKind: "hosted",
			envValues: map[string]string{},
			envGetErr: errors.New("env service unavailable"),
			wantErr:   true,
			wantCode:  exterrors.CodeEnvironmentValuesFailed,
		},
		{
			name:      "proceeds past ACR check when endpoint is set",
			agentKind: "hosted",
			envValues: map[string]string{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myregistry.azurecr.io",
			},
			// Package will continue past the ACR check and fail later
			// (no container service stub), but the ACR validation itself passes.
			wantErr:    true,
			wantCode:   "",
			wantNotACR: true,
		},
		{
			name:      "skips ACR check for prompt agents",
			agentKind: "prompt",
			envValues: map[string]string{},
			wantErr:   false,
			wantCode:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			var agentPath string
			if tt.agentKind == "hosted" {
				agentPath = writeHostedAgentYAML(t, dir)
			} else {
				agentPath = writePromptAgentYAML(t, dir)
			}

			envSrv := &packageEnvServer{
				values:  tt.envValues,
				getErr:  tt.envGetErr,
				envName: "test-env",
			}

			client := newPackageTestClient(t, envSrv)

			provider := &AgentServiceTargetProvider{
				azdClient:           client,
				agentDefinitionPath: agentPath,
				env:                 &azdext.Environment{Name: "test-env"},
			}

			result, err := provider.Package(
				t.Context(),
				&azdext.ServiceConfig{},
				&azdext.ServiceContext{},
				func(string) {},
			)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantNotACR {
					// Verify the error is NOT the ACR validation error,
					// confirming the check was passed successfully.
					var localErr *azdext.LocalError
					if errors.As(err, &localErr) {
						require.NotEqual(t, exterrors.CodeMissingContainerRegistryEndpoint, localErr.Code,
							"expected ACR check to pass, but got ACR missing error")
						require.NotEqual(t, exterrors.CodeEnvironmentValuesFailed, localErr.Code,
							"expected ACR check to pass, but got env values error")
					}
				} else {
					var localErr *azdext.LocalError
					require.True(t, errors.As(err, &localErr), "expected *azdext.LocalError, got %T", err)
					require.Equal(t, tt.wantCode, localErr.Code)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}
		})
	}
}
