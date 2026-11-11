// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// tenantHostEnv stands in for azd's environment service. GetValue falls back
// to process, as azd's does to its own process environment; GetValues returns
// only dotenv, as azd's does.
type tenantHostEnv struct {
	azdext.UnimplementedEnvironmentServiceServer
	name    string
	dotenv  map[string]string
	process map[string]string
}

func (s *tenantHostEnv) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.name}}, nil
}

func (s *tenantHostEnv) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	value, ok := s.dotenv[req.Key]
	if !ok {
		value = s.process[req.Key]
	}
	return &azdext.KeyValueResponse{Key: req.Key, Value: value}, nil
}

func (s *tenantHostEnv) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	resp := &azdext.KeyValueListResponse{}
	for k, v := range s.dotenv {
		resp.KeyValues = append(resp.KeyValues, &azdext.KeyValue{Key: k, Value: v})
	}
	return resp, nil
}

type tenantHostAccount struct {
	azdext.UnimplementedAccountServiceServer
	tenants map[string]string
}

func (s *tenantHostAccount) LookupTenant(
	_ context.Context, req *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: s.tenants[req.SubscriptionId]}, nil
}

// installAzdStandIn puts a fake azd first on PATH that records the arguments
// `azd auth token` was called with, and returns the file it writes them to.
func installAzdStandIn(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the azd stand-in")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > '" + argsFile + "'\n" +
		"echo '{\"token\":\"t\",\"expiresOn\":\"2099-01-01T00:00:00Z\"}'\n"
	// The stand-in has to be executable.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azd"), []byte(script), 0o700)) //nolint:gosec
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

// The tenant has to travel the whole way: resolved endpoint, tenant inference,
// credential, `azd auth token --tenant-id`. Each piece is tested on its own
// above; this guards the wiring in newEvalContext that joins them.
func TestNewEvalContextRequestsTheEnvironmentTenantOnlyForPersistedEndpoints(t *testing.T) {
	const (
		endpoint = "https://example.services.ai.azure.com/api/projects/p"
		tenant   = "22222222-2222-2222-2222-222222222222"
	)
	argsFile := installAzdStandIn(t)

	cases := map[string]struct {
		dotenv     map[string]string
		process    map[string]string
		flag       string
		hostEnv    string
		wantTenant bool
	}{
		"endpoint persisted in the environment": {
			dotenv:     map[string]string{projectEndpointEnvKey: endpoint, azdSubscriptionEnvKey: "sub-1"},
			wantTenant: true,
		},
		"endpoint only exported in azd's process environment": {
			dotenv:  map[string]string{azdSubscriptionEnvKey: "sub-1"},
			process: map[string]string{projectEndpointEnvKey: endpoint},
		},
		"--project-endpoint": {
			dotenv: map[string]string{projectEndpointEnvKey: endpoint, azdSubscriptionEnvKey: "sub-1"},
			flag:   "https://other.services.ai.azure.com/api/projects/q",
		},
		"host environment variable": {
			dotenv:  map[string]string{azdSubscriptionEnvKey: "sub-1"},
			hostEnv: endpoint,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(projectEndpointEnvKey, tc.hostEnv)
			t.Setenv(azureAIProjectEndpointEnvKey, "")

			server := grpc.NewServer()
			azdext.RegisterEnvironmentServiceServer(server, &tenantHostEnv{
				name: "dev", dotenv: tc.dotenv, process: tc.process,
			})
			azdext.RegisterAccountServiceServer(server, &tenantHostAccount{
				tenants: map[string]string{"sub-1": tenant},
			})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				_ = listener.Close()
			})
			t.Setenv("AZD_SERVER", listener.Addr().String())
			// Clear what an earlier case recorded.
			require.NoError(t, os.RemoveAll(argsFile))

			ec, err := newEvalContext(t.Context(), tc.flag)
			require.NoError(t, err)
			t.Cleanup(ec.Close)

			_, err = ec.cred.GetToken(t.Context(), policy.TokenRequestOptions{
				Scopes: []string{"https://ai.azure.com/.default"},
			})
			require.NoError(t, err)

			args, err := os.ReadFile(argsFile)
			require.NoError(t, err)
			if tc.wantTenant {
				assert.Contains(t, strings.TrimSpace(string(args)), "--tenant-id "+tenant)
			} else {
				assert.NotContains(t, string(args), "--tenant-id")
			}
		})
	}
}
