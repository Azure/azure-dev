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
		name       string
		values     map[string]string
		shell      map[string]string
		noEnv      bool
		noGlobal   bool
		want       string
		wantKeys   []string
		wantConfig bool
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
			want:     canonicalPersisted,
			wantKeys: []string{canonicalKey},
		},
		{
			name:     "canonical persisted value resolves without a shell value",
			values:   map[string]string{canonicalKey: canonicalPersisted},
			noGlobal: true,
			want:     canonicalPersisted,
			wantKeys: []string{canonicalKey},
		},
		{
			name:   "canonical shell value wins before legacy and global values",
			values: map[string]string{legacyKey: legacyPersisted},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want:     canonicalShell,
			wantKeys: []string{canonicalKey},
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
			want:     legacyPersisted,
			wantKeys: []string{canonicalKey, legacyKey},
		},
		{
			name:   "legacy shell value remains a fallback",
			values: map[string]string{canonicalKey: ""},
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			want:     legacyShell,
			wantKeys: []string{canonicalKey, legacyKey},
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
			want:       global,
			wantKeys:   []string{canonicalKey, legacyKey},
			wantConfig: true,
		},
		{
			name: "no active environment uses global config before shell",
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			noEnv:      true,
			want:       global,
			wantConfig: true,
		},
		{
			name: "no environment or global uses canonical shell value",
			shell: map[string]string{
				canonicalKey: canonicalShell,
				legacyKey:    legacyShell,
			},
			noEnv:      true,
			noGlobal:   true,
			want:       canonicalShell,
			wantConfig: true,
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
			noGlobal:   true,
			want:       canonicalShell,
			wantKeys:   []string{canonicalKey, legacyKey},
			wantConfig: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(canonicalKey, tt.shell[canonicalKey])
			t.Setenv(legacyKey, tt.shell[legacyKey])

			host := &precedenceHost{
				values:        tt.values,
				noEnv:         tt.noEnv,
				requestedKeys: make(chan string, 2),
			}
			config := &precedenceConfig{
				noGlobal:       tt.noGlobal,
				global:         global,
				requestedPaths: make(chan string, 2),
			}
			startEndpointHost(t, host, config)
			got, err := resolveProjectEndpoint(t.Context(), "")
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Endpoint)
			require.Len(t, host.requestedKeys, len(tt.wantKeys))
			for _, key := range tt.wantKeys {
				require.Equal(t, key, <-host.requestedKeys)
			}
			if tt.wantConfig {
				require.NotEmpty(t, config.requestedPaths)
			} else {
				require.Empty(t, config.requestedPaths)
			}
		})
	}
}

func TestEndpointLookupErrorsDoNotFallBack(t *testing.T) {
	const fallback = "https://fallback.services.ai.azure.com/api/projects/project"
	for _, tt := range []struct {
		key      string
		wantKeys []string
	}{
		{
			key:      foundryProjectEndpointEnvVar,
			wantKeys: []string{foundryProjectEndpointEnvVar},
		},
		{
			key:      legacyProjectEndpointEnvVar,
			wantKeys: []string{foundryProjectEndpointEnvVar, legacyProjectEndpointEnvVar},
		},
	} {
		for _, code := range []codes.Code{
			codes.Unavailable,
			codes.PermissionDenied,
			codes.NotFound,
			codes.Internal,
			codes.Canceled,
			codes.DeadlineExceeded,
		} {
			t.Run(tt.key+"/"+code.String(), func(t *testing.T) {
				t.Setenv(foundryProjectEndpointEnvVar, fallback)
				t.Setenv(legacyProjectEndpointEnvVar, fallback)
				host := &precedenceHost{
					values: map[string]string{
						foundryProjectEndpointEnvVar: "",
						legacyProjectEndpointEnvVar:  fallback,
					},
					readErrors:    map[string]error{tt.key: status.Error(code, "endpoint lookup failed")},
					requestedKeys: make(chan string, 2),
				}
				config := &precedenceConfig{
					global:         fallback,
					requestedPaths: make(chan string, 2),
				}
				startEndpointHost(t, host, config)

				got, err := resolveProjectEndpoint(t.Context(), "")
				require.ErrorContains(t, err, tt.key)
				require.ErrorContains(t, err, "dev")
				require.Equal(t, code, status.Code(err))
				require.Nil(t, got)
				require.Len(t, host.requestedKeys, len(tt.wantKeys))
				for _, key := range tt.wantKeys {
					require.Equal(t, key, <-host.requestedKeys)
				}
				require.Empty(t, config.requestedPaths)
			})
		}
	}
}

func startEndpointHost(t *testing.T, host *precedenceHost, config *precedenceConfig) {
	t.Helper()
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, host)
	azdext.RegisterUserConfigServiceServer(server, config)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("AZD_SERVER", listener.Addr().String())
}

type precedenceHost struct {
	azdext.UnimplementedEnvironmentServiceServer
	values        map[string]string
	noEnv         bool
	readErrors    map[string]error
	requestedKeys chan string
}

func (s *precedenceHost) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	if s.noEnv {
		return nil, status.Error(codes.NotFound, "no active environment")
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (s *precedenceHost) GetValue(_ context.Context, req *azdext.GetEnvRequest) (*azdext.KeyValueResponse, error) {
	s.requestedKeys <- req.Key
	if err := s.readErrors[req.Key]; err != nil {
		return nil, err
	}
	// Only absent persisted keys fall back to the host's shell.
	value, present := s.values[req.Key]
	if !present {
		value = os.Getenv(req.Key)
	}
	return &azdext.KeyValueResponse{Key: req.Key, Value: value}, nil
}

type precedenceConfig struct {
	azdext.UnimplementedUserConfigServiceServer
	noGlobal       bool
	global         string
	requestedPaths chan string
}

func (s *precedenceConfig) Get(
	_ context.Context, req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	s.requestedPaths <- req.Path
	if s.noGlobal || req.Path != "extensions.ai-projects.context" {
		return &azdext.GetUserConfigResponse{}, nil
	}
	return &azdext.GetUserConfigResponse{Found: true, Value: []byte(`{"endpoint":"` + s.global + `"}`)}, nil
}
