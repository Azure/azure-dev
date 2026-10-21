// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// hostedFeatureSources is one host snapshot for the Hosted Agent opt-in.
type hostedFeatureSources struct {
	alpha    map[string]any
	dotenv   map[string]string
	services []map[string]string
	noEnv    bool
}

type hostedFeatureConfigServer struct {
	azdext.UnimplementedUserConfigServiceServer
	alpha map[string]any
}

func (s *hostedFeatureConfigServer) Get(
	context.Context, *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	if s.alpha == nil {
		return &azdext.GetUserConfigResponse{}, nil
	}
	value, err := json.Marshal(s.alpha)
	if err != nil {
		return nil, err
	}
	return &azdext.GetUserConfigResponse{Found: true, Value: value}, nil
}

type hostedFeatureProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	services []map[string]string
}

func (s *hostedFeatureProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	project := &azdext.ProjectConfig{Name: "hosted", Services: map[string]*azdext.ServiceConfig{}}
	for i, env := range s.services {
		name := string(rune('a' + i))
		project.Services[name] = &azdext.ServiceConfig{Name: name, Host: "azure.ai.agent", Environment: env}
	}
	return &azdext.GetProjectResponse{Project: project}, nil
}

type hostedFeatureEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	dotenv map[string]string
	noEnv  bool
}

func (s *hostedFeatureEnvServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.noEnv {
		return nil, status.Error(codes.Unknown, "default environment not found")
	}
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.dotenv {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

// hostedFeatureOn is the alpha user config section that enables Hosted Agent.
var hostedFeatureOn = map[string]any{"agents": map[string]any{"hosted": "on"}}

// registerHostedFeatureOn enables Hosted Agent through user config for test
// servers. The selected environment is absent, which the opt-in read skips.
func registerHostedFeatureOn(srv *grpc.Server) {
	azdext.RegisterUserConfigServiceServer(srv, &hostedFeatureConfigServer{alpha: hostedFeatureOn})
	azdext.RegisterEnvironmentServiceServer(srv, &hostedFeatureEnvServer{noEnv: true})
}

func newHostedFeatureClient(t *testing.T, sources hostedFeatureSources) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterUserConfigServiceServer(srv, &hostedFeatureConfigServer{alpha: sources.alpha})
	azdext.RegisterProjectServiceServer(srv, &hostedFeatureProjectServer{services: sources.services})
	azdext.RegisterEnvironmentServiceServer(srv, &hostedFeatureEnvServer{dotenv: sources.dotenv, noEnv: sources.noEnv})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestIsHostedAgentFeatureEnabled(t *testing.T) {
	envVar := HostedAgentFeature.EnvVar
	on := map[string]string{envVar: "true"}
	off := map[string]string{envVar: "false"}

	tests := []struct {
		name    string
		process string
		sources hostedFeatureSources
		want    bool
	}{
		{name: "off by default", want: false},
		{name: "user config", sources: hostedFeatureSources{
			alpha: map[string]any{"agents": map[string]any{"hosted": "on"}},
		}, want: true},
		{name: "user config all", sources: hostedFeatureSources{alpha: map[string]any{"all": "ON"}}, want: true},
		{name: "process env", process: "true", want: true},
		{name: "dotenv", sources: hostedFeatureSources{dotenv: on}, want: true},
		{name: "service env", sources: hostedFeatureSources{services: []map[string]string{on}}, want: true},
		{name: "false never overrides true", process: "false", sources: hostedFeatureSources{
			alpha:    map[string]any{"agents": map[string]any{"hosted": "off"}},
			dotenv:   off,
			services: []map[string]string{off, on, off},
		}, want: true},
		{name: "all false", process: "false", sources: hostedFeatureSources{
			alpha:    map[string]any{"agents": map[string]any{"hosted": "off"}},
			dotenv:   off,
			services: []map[string]string{off},
		}, want: false},
		{name: "no environment", sources: hostedFeatureSources{
			noEnv: true, services: []map[string]string{on},
		}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envVar, tt.process)
			t.Setenv("AZD_ENVIRONMENT", "")
			enabled, err := IsHostedAgentFeatureEnabled(t.Context(), newHostedFeatureClient(t, tt.sources))
			require.NoError(t, err)
			require.Equal(t, tt.want, enabled)
		})
	}
}

func TestIsHostedAgentFeatureEnabledNilClient(t *testing.T) {
	t.Parallel()
	enabled, err := IsHostedAgentFeatureEnabled(t.Context(), nil)
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestIsHostedAgentFeatureEnabledPropagatesErrors(t *testing.T) {
	t.Parallel()
	// No services registered: the user config read fails.
	srv := grpc.NewServer()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	enabled, err := IsHostedAgentFeatureEnabled(t.Context(), client)
	require.False(t, enabled)
	require.ErrorContains(t, err, "checking hosted agent feature")
}

func TestRequireHostedAgentFeature(t *testing.T) {
	t.Setenv(HostedAgentFeature.EnvVar, "")
	t.Setenv("AZD_ENVIRONMENT", "")

	t.Run("enabled allows", func(t *testing.T) {
		client := newHostedFeatureClient(t, hostedFeatureSources{
			alpha: map[string]any{"agents": map[string]any{"hosted": "on"}},
		})
		require.NoError(t, RequireHostedAgentFeature(t.Context(), client))
	})

	t.Run("disabled returns compatibility error", func(t *testing.T) {
		err := RequireHostedAgentFeature(t.Context(), newHostedFeatureClient(t, hostedFeatureSources{}))
		require.Error(t, err)
		var local *azdext.LocalError
		require.True(t, errors.As(err, &local))
		require.Equal(t, exterrors.CodeFeatureNotEnabled, local.Code)
		require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
		require.Contains(t, local.Message, "hosted agent support is disabled")
		require.Contains(t, local.Suggestion, "azd config set alpha.agents.hosted on")
		require.Contains(t, local.Suggestion, HostedAgentFeature.EnvVar)
	})
}
