// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type confirmServer struct {
	azdext.UnimplementedPromptServiceServer
	confirm func(context.Context, *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error)
}

func (s *confirmServer) Confirm(
	ctx context.Context, request *azdext.ConfirmRequest,
) (*azdext.ConfirmResponse, error) {
	return s.confirm(ctx, request)
}

func TestServiceSessionUsesHostAndRESTClient(t *testing.T) {
	for _, mode := range []string{"yes", "no", "missing decision", "host failure"} {
		t.Run(mode, func(t *testing.T) {
			startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
				return nil, status.Error(codes.Internal, "deployment must not be queried")
			}}, &confirmServer{confirm: func(
				ctx context.Context, request *azdext.ConfirmRequest,
			) (*azdext.ConfirmResponse, error) {
				md, _ := metadata.FromIncomingContext(ctx)
				assert.Equal(t, []string{"private-host-token"}, md.Get("authorization"))
				if assert.NotNil(t, request.Options) {
					assert.Equal(t, "Delete analyzer?", request.Options.Message)
					if assert.NotNil(t, request.Options.DefaultValue) {
						assert.False(t, *request.Options.DefaultValue)
					}
				}
				if mode == "host failure" {
					return nil, status.Error(codes.Unavailable, "host detail")
				}
				if mode == "missing decision" {
					return &azdext.ConfirmResponse{}, nil
				}
				return &azdext.ConfirmResponse{Value: new(mode == "yes")}, nil
			}})
			cmd := &cobra.Command{}
			cmd.SetContext(azdext.WithAccessToken(t.Context()))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			session, err := openServiceSession(cmd, &azdext.ExtensionContext{}, config.Options{
				Endpoint: "https://configured.services.ai.azure.com", TenantID: "configured-tenant",
				APIVersion: cuclient.PreviewAPIVersion, DefaultAnalyzer: "configured-analyzer",
				ModelDeployments: map[string]string{"gpt-5.2": "configured-deployment"},
				SubscriptionID:   "selected-subscription", ResourceGroup: "selected-group",
			})
			require.NoError(t, err)
			defer session.close()
			assert.IsType(t, &cuclient.RESTTransport{}, session.client)
			assert.Equal(t, cuclient.PreviewAPIVersion, session.apiVersion)
			assert.Equal(t, "configured-analyzer", session.analyzer)
			assert.Equal(t, "https://configured.services.ai.azure.com/contentunderstanding", session.endpoint)
			assert.Empty(t, session.environment)
			assert.Equal(t, map[string]string{"gpt-5.2": "configured-deployment"}, session.models)
			assert.Equal(t, "selected-subscription", session.scope.SubscriptionID)
			assert.Equal(t, "selected-group", session.scope.ResourceGroup)
			copyClient, err := session.clientForEndpoint("https://destination.services.ai.azure.com")
			require.NoError(t, err)
			assert.IsType(t, &cuclient.RESTTransport{}, copyClient)
			_, err = session.clientForEndpoint("http://unsafe.example")
			require.Error(t, err)
			cancelled, cancel := context.WithCancel(cmd.Context())
			cancel()
			_, err = session.resolveAccount(cancelled, "source-account", "", "")
			require.ErrorIs(t, err, context.Canceled)
			confirmed, err := session.confirm(cmd.Context(), "Delete analyzer?")
			if mode == "missing decision" || mode == "host failure" {
				require.Error(t, err)
				assert.False(t, confirmed)
			} else {
				require.NoError(t, err)
				assert.Equal(t, mode == "yes", confirmed)
			}
			assert.Empty(t, stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestServiceSessionConfigurationFailure(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	session, err := openServiceSession(cmd, &azdext.ExtensionContext{}, config.Options{})
	require.Error(t, err)
	assert.Nil(t, session)
}

func TestServiceSessionRejectsUntrustedEnvironmentBeforeAuthentication(t *testing.T) {
	startHost(t, &deploymentServer{get: func(context.Context) (*azdext.GetDeploymentContextResponse, error) {
		t.Error("invalid endpoints must fail before loading deployment context")
		return nil, status.Error(codes.Internal, "unexpected deployment lookup")
	}})
	argsPath := installAuthenticationCLI(t, true)
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	session, err := openServiceSession(cmd, &azdext.ExtensionContext{Environment: "untrusted"}, config.Options{})
	require.ErrorContains(t, err, "Azure resource endpoint")
	assert.Nil(t, session)
	assert.NotContains(t, err.Error(), "private")
	assert.NoFileExists(t, argsPath, "an invalid environment endpoint must not invoke azd auth token")
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}
