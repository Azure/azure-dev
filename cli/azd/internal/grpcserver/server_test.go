// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// This test file verifies that the gRPC server properly handles authenticated and unauthenticated requests.
// It checks that the server starts correctly, returns the appropriate server information,
// and enforces authentication requirements for accessing services.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Test_Server_Start validates the start and stop flows of the gRPC server,
// and confirms the expected behavior for authenticated and unauthenticated requests.
func Test_Server_Start(t *testing.T) {
	server := NewServer(
		azdext.UnimplementedProjectServiceServer{},
		azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{},
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		azdext.UnimplementedEventServiceServer{},
		azdext.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
	)

	serverInfo, err := server.Start()
	require.NotNil(t, serverInfo)
	require.NoError(t, err)
	defer func() {
		err := server.Stop()
		require.NoError(t, err)
	}()

	extension := &extensions.Extension{
		Id: "azd.internal.test",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
		},
		Namespace: "test",
	}

	t.Run("ValidToken", func(t *testing.T) {
		// Test for a valid extension token: expect service calls to be unimplemented (authenticated case).
		accessToken, err := GenerateExtensionToken(extension, serverInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(context.Background(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)

		// Expect the service to be unimplemented since we are using mock service implementations.
		require.Equal(t, codes.Unimplemented, st.Code())
	})

	t.Run("InvalidToken", func(t *testing.T) {
		// Test for a valid extension token: expect service calls to be unimplemented (authenticated case).
		invalidServerInfo := &ServerInfo{
			Address:    serverInfo.Address,
			Port:       serverInfo.Port,
			SigningKey: []byte("invalid"),
		}
		accessToken, err := GenerateExtensionToken(extension, invalidServerInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(context.Background(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("MissingToken", func(t *testing.T) {
		// Test for missing authentication token: expect Unauthenticated error.
		ctx := context.Background()
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unauthenticated, st.Code())
	})
}

func Test_wrapErrorWithSuggestion(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		wantNil          bool
		wantContain      string
		wantSameInstance bool
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			wantNil: true,
		},
		{
			name:             "error without suggestion is returned as-is",
			err:              errors.New("some error"),
			wantContain:      "some error",
			wantSameInstance: true,
		},
		{
			name: "error with suggestion includes suggestion text",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("authentication failed"),
				Suggestion: "Suggestion: run `azd auth login` to acquire a new token.",
			},
			wantContain: "azd auth login",
		},
		{
			name: "wrapped error with suggestion includes suggestion text",
			err: fmt.Errorf("failed to prompt: %w", &internal.ErrorWithSuggestion{
				Err:        errors.New("token expired"),
				Suggestion: "Suggestion: login expired, run `azd auth login` to acquire a new token.",
			}),
			wantContain: "azd auth login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapErrorWithSuggestion(tt.err)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Contains(t, result.Error(), tt.wantContain)
			if tt.wantSameInstance {
				require.Same(t, tt.err, result, "expected error to be returned unchanged (same instance)")
			}
		})
	}
}
