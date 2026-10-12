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
	const canonicalKey = "FOUNDRY_PROJECT_ENDPOINT"
	const legacyKey = "AZURE_AI_PROJECT_ENDPOINT"
	const canonicalShell = "https://canonical-shell.services.ai.azure.com/api/projects/project"
	const legacyShell = "https://legacy-shell.services.ai.azure.com/api/projects/project"
	const canonicalPersisted = "https://canonical-persisted.services.ai.azure.com/api/projects/project"
	const legacyPersisted = "https://legacy-persisted.services.ai.azure.com/api/projects/project"
	const global = "https://global.services.ai.azure.com/api/projects/project"
	t.Setenv("AZD_SERVER", "")
	for _, tt := range []struct {
		name     string
		values   map[string]string
		shell    map[string]string
		noEnv    bool
		noGlobal bool
		want     string
	}{
		{
			name: "canonical persisted value wins",
			values: map[string]string{
				canonicalKey: canonicalPersisted,
				legacyKey:    legacyPersisted,
			},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want: canonicalPersisted,
		},
		{
			name:     "canonical persisted value resolves without a shell value",
			values:   map[string]string{canonicalKey: canonicalPersisted},
			noGlobal: true,
			want:     canonicalPersisted,
		},
		{
			name:   "canonical shell value wins before legacy and global values",
			values: map[string]string{legacyKey: legacyPersisted},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want: canonicalShell,
		},
		{
			name: "legacy persisted value is used when canonical is empty",
			values: map[string]string{
				canonicalKey: "",
				legacyKey:    legacyPersisted,
			},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want: legacyPersisted,
		},
		{
			name:   "legacy shell value remains a fallback",
			values: map[string]string{canonicalKey: ""},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want: legacyShell,
		},
		{
			name: "empty values defer to global config",
			values: map[string]string{
				canonicalKey: "",
				legacyKey:    "",
			},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want: global,
		},
		{
			name: "no active environment uses global config before shell",
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			noEnv: true,
			want:  global,
		},
		{
			name: "no environment or global uses canonical shell value",
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			noEnv:    true,
			noGlobal: true,
			want:     canonicalShell,
		},
		{
			name: "empty values without global config use canonical shell value",
			values: map[string]string{
				canonicalKey: "",
				legacyKey:    "",
			},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			noGlobal: true,
			want:     canonicalShell,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(canonicalKey, tt.shell[canonicalKey])
			t.Setenv(legacyKey, tt.shell[legacyKey])

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
