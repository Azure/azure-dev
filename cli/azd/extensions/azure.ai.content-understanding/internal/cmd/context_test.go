// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type deploymentServer struct {
	azdext.UnimplementedDeploymentServiceServer
	azdext.UnimplementedAccountServiceServer
	get    func(context.Context) (*azdext.GetDeploymentContextResponse, error)
	lookup func(context.Context, *azdext.LookupTenantRequest) (*azdext.LookupTenantResponse, error)
}

func (s *deploymentServer) GetDeploymentContext(
	ctx context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetDeploymentContextResponse, error) {
	return s.get(ctx)
}

func (s *deploymentServer) LookupTenant(
	ctx context.Context, request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	if s.lookup == nil {
		return nil, status.Error(codes.Unimplemented, "unexpected tenant lookup")
	}
	return s.lookup(ctx, request)
}

type environmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
}

func (*environmentServer) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (*environmentServer) Get(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: request.Name}}, nil
}

func (*environmentServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	values := []*azdext.KeyValue{
		{Key: "CU_ENDPOINT", Value: "https://selected.services.ai.azure.com"},
	}
	if request.Name == "untrusted" {
		values[0].Value = "https://private-attacker.example"
	}
	if request.Name == "test" {
		values = append(values, &azdext.KeyValue{Key: "AZURE_TENANT_ID", Value: "named-tenant"})
	}
	if request.Name == "subscription" {
		values = append(values, &azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"})
	}
	return &azdext.KeyValueListResponse{KeyValues: values}, nil
}

func (*environmentServer) GetConfigSection(
	context.Context, *azdext.GetConfigSectionRequest,
) (*azdext.GetConfigSectionResponse, error) {
	return &azdext.GetConfigSectionResponse{}, nil
}

func startHost(t *testing.T, server *deploymentServer, prompts ...azdext.PromptServiceServer) {
	t.Helper()
	for key := range config.ProcessValues() {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host := grpc.NewServer()
	azdext.RegisterDeploymentServiceServer(host, server)
	azdext.RegisterEnvironmentServiceServer(host, &environmentServer{})
	azdext.RegisterAccountServiceServer(host, server)
	azdext.RegisterUserConfigServiceServer(host, &userConfigServer{})
	if len(prompts) != 0 {
		azdext.RegisterPromptServiceServer(host, prompts[0])
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- host.Serve(listener) }()
	t.Cleanup(func() {
		host.Stop()
		if err := <-serveErr; err != nil {
			assert.ErrorIs(t, err, grpc.ErrServerStopped)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
}

func installAuthenticationCLI(t *testing.T, authenticated bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "azd authentication")
	require.NoError(t, os.Mkdir(dir, 0o700))
	argsPath := filepath.Join(dir, "args.txt")
	t.Setenv("CU_TEST_AZD_ARGS", argsPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	name := "azd"
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CU_TEST_AZD_ARGS\"\n"
	if runtime.GOOS == "windows" {
		name = "azd.cmd"
		script = "@echo off\r\necho %* > \"%CU_TEST_AZD_ARGS%\"\r\n"
	}
	if authenticated {
		if runtime.GOOS == "windows" {
			script += "echo {\"token\":\"test-access-token\",\"expiresOn\":\"2099-01-01T00:00:00Z\"}\r\n"
		} else {
			script += "printf '%s\\n' '{\"token\":\"test-access-token\",\"expiresOn\":\"2099-01-01T00:00:00Z\"}'\n"
		}
	} else if runtime.GOOS == "windows" {
		script += "echo private-auth-detail 1>&2\r\nexit /b 1\r\n"
	} else {
		script += "printf '%s\\n' 'private-auth-detail' >&2\nexit 1\n"
	}
	// #nosec G306 -- Private executable test host.
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700))
	return argsPath
}

func assertRuntimeAuthentication(t *testing.T, r *runtimeContext, tenant string) {
	t.Helper()
	argsPath := installAuthenticationCLI(t, true)
	token, err := r.Credential.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"https://cognitiveservices.azure.com/.default"},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-access-token", token.Token)
	args, err := os.ReadFile(argsPath) // #nosec G304 -- Reads only this test's private fake-host argument log.
	require.NoError(t, err)
	expected := []string{"auth", "token", "-o", "json", "--no-prompt",
		"--scope", "https://cognitiveservices.azure.com/.default"}
	if tenant != "" {
		expected = append(expected, "--tenant-id", tenant)
	}
	assert.Equal(t, expected, strings.Fields(string(args)))
}

func TestRuntimeContext(t *testing.T) {
	startHost(t, &deploymentServer{get: func(ctx context.Context) (*azdext.GetDeploymentContextResponse, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if values := md.Get("authorization"); len(values) != 1 || values[0] != "private-host-token" {
			return nil, status.Error(codes.Unauthenticated, "host authorization missing")
		}
		return &azdext.GetDeploymentContextResponse{AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{TenantId: "selected-tenant"},
		}}, nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Debug: true}, config.Options{})
	require.NoError(t, err)
	defer runtime.Close()
	assert.Equal(t, "selected-tenant", runtime.Config.TenantID)
	assertRuntimeAuthentication(t, runtime, "selected-tenant")
	assert.Equal(t, "dev", runtime.Config.Environment)
	assert.Equal(t, "https://selected.services.ai.azure.com/contentunderstanding", runtime.Config.Endpoint)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "Initialized azd runtime")
	assert.NotContains(t, stderr.String(), "private-host-token")
	assert.NotContains(t, stderr.String(), "selected-tenant")
	cancel()
	assert.ErrorIs(t, runtime.Context.Err(), context.Canceled)
}

func TestMissingHostContext(t *testing.T) {
	for _, tt := range []struct{ server, token string }{
		{"", ""}, {"127.0.0.1:1234", ""}, {"", "private-token"},
	} {
		t.Run(tt.server+"/"+tt.token, func(t *testing.T) {
			t.Setenv("AZD_SERVER", tt.server)
			t.Setenv("AZD_ACCESS_TOKEN", tt.token)
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{}, config.Options{})
			require.Nil(t, runtime)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeHostUnavailable, local.Code)
			assert.Contains(t, local.Suggestion, "azd ai cu")
		})
	}
}

func TestRuntimeHostFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		get  func(context.Context) (*azdext.GetDeploymentContextResponse, error)
		code string
	}{
		{"missing auth", func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
			return nil, status.Error(codes.Unauthenticated, "private-auth-detail")
		}, exterrors.CodeAuthenticationFailed},
		{"deadline during RPC", func(ctx context.Context) (*azdext.GetDeploymentContextResponse, error) {
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}, exterrors.CodeDeadlineExceeded},
		{"missing tenant", func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
			return &azdext.GetDeploymentContextResponse{}, nil
		}, exterrors.CodeMissingTenant},
	} {
		t.Run(tt.name, func(t *testing.T) {
			startHost(t, &deploymentServer{get: tt.get})
			cmd := &cobra.Command{}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			cmd.SetContext(ctx)
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Debug: true}, config.Options{})
			require.Nil(t, runtime)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, tt.code, local.Code)
			assert.NotContains(t, stderr.String(), "private-auth-detail")
			assert.NotContains(t, stderr.String(), "private-host-token")
			assert.Empty(t, stdout.String())
		})
	}
}

func TestRuntimeAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{}, config.Options{})
	assert.Nil(t, runtime)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRuntimeExplicitTenant(t *testing.T) {
	startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
		return nil, status.Error(codes.Internal, "deployment context should not be queried")
	}})
	t.Setenv("AZD_DEBUG", "false")
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{}, config.Options{
		Endpoint: "https://explicit.services.ai.azure.com", TenantID: "explicit-tenant",
	})
	require.NoError(t, err)
	defer runtime.Close()
	assert.Equal(t, "explicit-tenant", runtime.Config.TenantID)
	assertRuntimeAuthentication(t, runtime, "explicit-tenant")
	assert.Empty(t, runtime.Config.Environment)
	assert.Empty(t, stderr.String())
}

func TestRuntimeCancellationDuringRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	startHost(t, &deploymentServer{get: func(ctx context.Context) (*azdext.GetDeploymentContextResponse, error) {
		cancel()
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{}, config.Options{})
	assert.Nil(t, runtime)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRuntimeNamedEnvironmentCredential(t *testing.T) {
	startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
		return nil, status.Error(codes.Internal, "default deployment must not supply a named environment's tenant")
	}})
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Environment: "test", Debug: true}, config.Options{})
	require.NoError(t, err)
	defer runtime.Close()
	assert.Equal(t, "test", runtime.Config.Environment)
	assert.Equal(t, "named-tenant", runtime.Config.TenantID)
	assertRuntimeAuthentication(t, runtime, "named-tenant")
	assert.NotContains(t, stderr.String(), "named-tenant")
	assert.NotContains(t, stderr.String(), "private-host-token")
}

func TestRuntimeProjectlessDefaultAuthentication(t *testing.T) {
	for _, source := range []string{"option", "process", "inside project"} {
		t.Run(source, func(t *testing.T) {
			startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
				t.Error("projectless authentication must not query deployment context")
				return nil, errors.New("unexpected deployment call")
			}})
			options := config.Options{Endpoint: "https://explicit.services.ai.azure.com"}
			if source == "process" {
				options.Endpoint = ""
				t.Setenv("CU_ENDPOINT", "https://process.services.ai.azure.com")
			}
			if source == "inside project" {
				t.Chdir(t.TempDir())
				require.NoError(t, os.WriteFile("azure.yaml", []byte("name: unrelated-project\n"), 0o600))
			}
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			r, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Debug: true}, options)
			require.NoError(t, err)
			defer r.Close()
			assert.Empty(t, r.Config.TenantID)
			assert.Empty(t, r.Config.Environment)
			assertRuntimeAuthentication(t, r, "")
			assert.Empty(t, stdout.String())
			assert.NotContains(t, stderr.String(), "test-access-token")
			assert.NotContains(t, stderr.String(), "private-host-token")
		})
	}
}

func TestRuntimeSelectedSubscriptionCredential(t *testing.T) {
	startHost(t, &deploymentServer{
		get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
			t.Error("selected environment must not query the default deployment")
			return nil, errors.New("unexpected deployment call")
		},
		lookup: func(ctx context.Context, request *azdext.LookupTenantRequest) (*azdext.LookupTenantResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			assert.Equal(t, []string{"private-host-token"}, md.Get("authorization"))
			assert.Equal(t, "selected-sub", request.SubscriptionId)
			return &azdext.LookupTenantResponse{TenantId: "user-access-tenant"}, nil
		},
	})
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	r, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Environment: "subscription"}, config.Options{})
	require.NoError(t, err)
	defer r.Close()
	assert.Equal(t, "subscription", r.Config.Environment)
	assert.Equal(t, "user-access-tenant", r.Config.TenantID)
	assertRuntimeAuthentication(t, r, "user-access-tenant")
}

func TestRuntimeAuthenticationRequired(t *testing.T) {
	for _, tenant := range []string{"", "explicit-tenant"} {
		t.Run(tenant, func(t *testing.T) {
			startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
				t.Error("projectless authentication must not query deployment context")
				return nil, errors.New("unexpected deployment call")
			}})
			installAuthenticationCLI(t, false)
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			r, err := newRuntimeContext(cmd, &azdext.ExtensionContext{}, config.Options{
				Endpoint: "https://explicit.services.ai.azure.com", TenantID: tenant,
			})
			require.NoError(t, err)
			defer r.Close()
			client, err := cuclient.NewRESTTransport(r.Config.Endpoint, r.Credential, nil)
			require.NoError(t, err)
			_, err = client.GetDefaults(t.Context())
			require.Error(t, err)
			local, ok := errors.AsType[*azdext.LocalError](exterrors.FromError(err))
			require.True(t, ok)
			assert.Equal(t, "authentication_failed", local.Code)
			assert.Contains(t, local.Suggestion, "azd auth login")
			assert.Contains(t, local.Suggestion, "AZURE_TENANT_ID")
			assert.NotContains(t, local.Error(), "private-auth-detail")
			assert.NotContains(t, local.Error(), "private-host-token")
		})
	}
}

func TestRuntimeConfigurationFailureRedaction(t *testing.T) {
	startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
		return nil, status.Error(codes.Internal, "deployment must not be queried")
	}})
	t.Setenv("CU_API_KEY", "private-api-key")
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	runtime, err := newRuntimeContext(cmd, &azdext.ExtensionContext{Debug: true},
		config.Options{Endpoint: "https://explicit.services.ai.azure.com", TenantID: "explicit-tenant"})
	require.Nil(t, runtime)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, "unsupported_authentication", local.Code)
	assert.NotContains(t, err.Error()+stderr.String(), "private-api-key")
	assert.NotContains(t, stderr.String(), "private-host-token")
	assert.NotContains(t, stderr.String(), "explicit-tenant")
	assert.Empty(t, stdout.String())
}
