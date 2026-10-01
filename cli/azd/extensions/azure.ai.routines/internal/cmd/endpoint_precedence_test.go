// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEndpointPrecedenceWithHostShellFallback(t *testing.T) {
	const key = "AZURE_AI_PROJECT_ENDPOINT"
	const shell = "https://shell.services.ai.azure.com/api/projects/project"
	const persisted = "https://persisted.services.ai.azure.com/api/projects/project"
	const global = "https://global.services.ai.azure.com/api/projects/project"
	const foundry = "https://foundry.services.ai.azure.com/api/projects/project"
	t.Setenv("AZD_SERVER", "")
	t.Setenv(key, shell)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", foundry)
	for _, tt := range []struct {
		name     string
		values   map[string]string
		noEnv    bool
		noGlobal bool
		want     string
	}{
		{"persisted wins", map[string]string{key: persisted}, false, false, persisted},
		{"missing legacy key uses shell before global and canonical shell", nil, false, false, shell},
		{"empty key defers to global", map[string]string{key: ""}, false, false, global},
		{"no active environment uses global first", nil, true, false, global},
		{"no environment or global uses canonical shell not legacy", nil, true, true, foundry},
		{"empty key without global reaches canonical shell", map[string]string{key: ""}, false, true, foundry},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := &precedenceHost{values: tt.values, noEnv: tt.noEnv}
			server := grpc.NewServer()
			azdext.RegisterEnvironmentServiceServer(server, host)
			azdext.RegisterUserConfigServiceServer(server, &precedenceConfig{noGlobal: tt.noGlobal, global: global})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			got, err := resolveProjectEndpoint(t.Context(), "")
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Endpoint)
		})
	}
}

type precedenceHost struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
	noEnv  bool
}

func (s *precedenceHost) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	if s.noEnv {
		return nil, status.Error(codes.NotFound, "no active environment")
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (s *precedenceHost) GetValue(_ context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
	// Mirror the host's Environment.Getenv contract: only absent keys fall back to the process environment.
	value, present := s.values[req.Key]
	if !present {
		value = os.Getenv(req.Key)
	}
	return &azdext.KeyValueResponse{Key: req.Key, Value: value}, nil
}

type precedenceConfig struct {
	azdext.UnimplementedUserConfigServiceServer
	noGlobal bool
	global   string
}

func (s *precedenceConfig) Get(
	_ context.Context, req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	if s.noGlobal || req.Path != "extensions.ai-projects.context" {
		return &azdext.GetUserConfigResponse{}, nil
	}
	return &azdext.GetUserConfigResponse{Found: true, Value: []byte(`{"endpoint":"` + s.global + `"}`)}, nil
}
