// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type invokeEnvironmentFailureServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	err error
}

func (s *invokeEnvironmentFailureServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return nil, s.err
}

func TestDirectInvokeWithoutDefaultEnvironment(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, global := range []bool{false, true} {
		for _, protocol := range []string{"responses", "invocations", "a2a"} {
			t.Run(protocol+map[bool]string{false: "/process", true: "/global"}[global], func(t *testing.T) {
				project := &helpersProjectServer{project: &azdext.ProjectConfig{
					Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
						"local-service": {Name: "local-service", Host: AiAgentHost},
					},
				}}
				// Match the host's plain sentinel crossing gRPC, not a synthetic success response.
				env := &invokeEnvironmentFailureServer{err: errors.New("default environment not found")}
				t.Setenv("AZD_SERVER", newInvokeRemoteContextTestAzdServer(t, project, env))
				if global {
					t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
					client, err := azdext.NewAzdClient()
					require.NoError(t, err)
					t.Cleanup(func() { client.Close() })
					_, err = client.UserConfig().Set(t.Context(), &azdext.SetUserConfigRequest{
						Path: projectsExtensionContextPath, Value: []byte(`{"endpoint":"` + endpoint + `"}`),
					})
					require.NoError(t, err)
				} else {
					t.Setenv("FOUNDRY_PROJECT_ENDPOINT", endpoint)
				}
				action := &InvokeAction{flags: &invokeFlags{name: "remote-agent", protocol: protocol}, noPrompt: true}
				rc, err := action.resolveRemoteContext(t.Context())
				require.NoError(t, err)
				t.Cleanup(func() { rc.azdClient.Close() })
				require.Equal(t, "remote-agent", rc.name)
				require.Equal(t, endpoint, rc.projectEndpoint)
				require.Empty(t, rc.serviceName)
			})
		}
	}
}

func TestDeployedLookupPreservesRealEnvironmentErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"transport", status.Error(codes.Unavailable, "host unavailable")},
		{"cancelled", status.Error(codes.Canceled, "cancelled")},
		{"malformed", status.Error(codes.Unknown, "invalid environment JSON")},
		{"named-file-missing", status.Error(codes.NotFound, "environment file missing")},
		{"lookalike", status.Error(codes.Unknown, "reading state: default environment not found")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{},
			}}
			t.Setenv("AZD_SERVER", newInvokeRemoteContextTestAzdServer(t, project,
				&invokeEnvironmentFailureServer{err: tt.err}))
			t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://account.services.ai.azure.com/api/projects/project")
			action := &InvokeAction{flags: &invokeFlags{name: "remote-agent", protocol: "responses"}}
			_, err := action.resolveRemoteContext(t.Context())
			require.Error(t, err)
			require.Contains(t, err.Error(), status.Convert(tt.err).Message())
			require.False(t, isDefaultEnvironmentMissing(err))
		})
	}
	// Other callers opting into strict deployed-name lookup retain the original error.
	project := &helpersProjectServer{project: &azdext.ProjectConfig{Path: t.TempDir()}}
	client := newHelpersTestAzdClient(t, project, &helpersPromptServer{},
		&invokeEnvironmentFailureServer{err: errors.New("default environment not found")})
	_, err := resolveAgentServiceFromProject(t.Context(), client, "remote-agent", true, withDeployedAgentNameLookup())
	require.ErrorContains(t, err, "failed to get current environment")
	_, notFound := errors.AsType[agentServiceLookupNotFoundError](err)
	require.False(t, notFound)
}
