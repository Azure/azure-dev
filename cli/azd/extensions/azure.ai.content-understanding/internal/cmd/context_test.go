// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/exterrors"

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
	get func(context.Context) (*azdext.GetDeploymentContextResponse, error)
}

func (s *deploymentServer) GetDeploymentContext(
	ctx context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetDeploymentContextResponse, error) {
	return s.get(ctx)
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
		{Key: "CU_ENDPOINT", Value: "https://selected.example"},
	}
	if request.Name == "test" {
		values = append(values, &azdext.KeyValue{Key: "AZURE_TENANT_ID", Value: "named-tenant"})
	}
	return &azdext.KeyValueListResponse{KeyValues: values}, nil
}

func (*environmentServer) GetConfigSection(
	context.Context, *azdext.GetConfigSectionRequest,
) (*azdext.GetConfigSectionResponse, error) {
	return &azdext.GetConfigSectionResponse{}, nil
}

func startHost(t *testing.T, server *deploymentServer) {
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
	serveErr := make(chan error, 1)
	go func() { serveErr <- host.Serve(listener) }()
	t.Cleanup(func() {
		host.Stop()
		assert.NoError(t, <-serveErr)
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
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
	assert.Equal(t, "selected-tenant", runtime.Credential.TenantID())
	assert.Equal(t, runtime.Config.TenantID, runtime.Credential.TenantID())
	assert.Equal(t, "dev", runtime.Config.Environment)
	assert.Equal(t, "https://selected.example/contentunderstanding", runtime.Config.Endpoint)
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
		Endpoint: "https://explicit.example", TenantID: "explicit-tenant",
	})
	require.NoError(t, err)
	defer runtime.Close()
	assert.Equal(t, "explicit-tenant", runtime.Credential.TenantID())
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
	assert.Equal(t, runtime.Config.TenantID, runtime.Credential.TenantID())
	assert.NotContains(t, stderr.String(), "named-tenant")
	assert.NotContains(t, stderr.String(), "private-host-token")
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
		config.Options{Endpoint: "https://explicit.example", TenantID: "explicit-tenant"})
	require.Nil(t, runtime)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, "unsupported_authentication", local.Code)
	assert.NotContains(t, err.Error()+stderr.String(), "private-api-key")
	assert.NotContains(t, stderr.String(), "private-host-token")
	assert.NotContains(t, stderr.String(), "explicit-tenant")
	assert.Empty(t, stdout.String())
}
