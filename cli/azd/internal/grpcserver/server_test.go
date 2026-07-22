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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
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
		azdext.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		NewTelemetryService(),
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

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
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

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("MissingToken", func(t *testing.T) {
		// Test for missing authentication token: expect Unauthenticated error.
		ctx := t.Context()
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Project().Get(ctx, &azdext.EmptyRequest{})
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("TelemetryAccepted", func(t *testing.T) {
		tracing.ResetCommandUsageForTest()
		t.Cleanup(tracing.ResetCommandUsageForTest)

		stExtension := &extensions.Extension{
			Id: "azd.internal.telemetry",
			Capabilities: []extensions.CapabilityType{
				extensions.ServiceTargetProviderCapability,
			},
			Namespace: "test",
		}
		accessToken, err := GenerateExtensionToken(stExtension, serverInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		// The server runs in-process, so the handler writes to this scope.
		scope := tracing.BeginCommandUsageScope("cmd.deploy")

		resp, err := client.Telemetry().AddCommandUsageAttribute(ctx, &azdext.AddCommandUsageAttributeRequest{
			Key:   telemetry.AgentDeploymentModeAttribute,
			Value: string(telemetry.AgentDeploymentModeCode),
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		attrs, err := tracing.CloseCommandUsageScope(scope)
		require.NoError(t, err)
		require.Len(t, attrs, 1)
		require.Equal(t, []string{"code"}, attrs[0].Value.AsStringSlice())
	})

	t.Run("TelemetryMissingCapability", func(t *testing.T) {
		// The base extension only declares CustomCommandCapability.
		accessToken, err := GenerateExtensionToken(extension, serverInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Telemetry().AddCommandUsageAttribute(ctx, &azdext.AddCommandUsageAttributeRequest{
			Key:   telemetry.AgentDeploymentModeAttribute,
			Value: string(telemetry.AgentDeploymentModeCode),
		})
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("TelemetryMissingToken", func(t *testing.T) {
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)

		_, err = client.Telemetry().AddCommandUsageAttribute(
			t.Context(),
			&azdext.AddCommandUsageAttributeRequest{
				Key:   telemetry.AgentDeploymentModeAttribute,
				Value: string(telemetry.AgentDeploymentModeCode),
			},
		)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unauthenticated, st.Code())
	})
}

// Test_Server_StreamInterceptor validates that the streaming RPC interceptor
// enforces authentication the same way as the unary interceptor.
func Test_Server_StreamInterceptor(t *testing.T) {
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
		azdext.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		azdext.UnimplementedTelemetryServiceServer{},
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
		accessToken, err := GenerateExtensionToken(extension, serverInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)
		defer client.Close()

		stream, err := client.Events().EventStream(ctx)
		// With a valid token, the stream should open successfully.
		// The underlying service is unimplemented, so we may get Unimplemented on Send/Recv,
		// but the stream itself should be created without an auth error.
		if err != nil {
			st, ok := status.FromError(err)
			require.True(t, ok)
			// Unimplemented is acceptable (mock service), but Unauthenticated is not.
			require.NotEqual(t, codes.Unauthenticated, st.Code(),
				"valid token should not get Unauthenticated")
		} else {
			require.NotNil(t, stream)
			// Try to close the send side; any error should be Unimplemented, not Unauthenticated.
			err = stream.CloseSend()
			if err != nil {
				st, ok := status.FromError(err)
				if ok {
					require.NotEqual(t, codes.Unauthenticated, st.Code())
				}
			}
		}
	})

	t.Run("MissingToken", func(t *testing.T) {
		ctx := t.Context()
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)
		defer client.Close()

		stream, err := client.Events().EventStream(ctx)
		if err != nil {
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.Unauthenticated, st.Code())
		} else {
			// For bidi streams, the auth error may surface on Recv rather than stream creation.
			_, recvErr := stream.Recv()
			require.Error(t, recvErr)
			st, ok := status.FromError(recvErr)
			require.True(t, ok)
			require.Equal(t, codes.Unauthenticated, st.Code())
		}
	})

	t.Run("InvalidToken", func(t *testing.T) {
		invalidServerInfo := &ServerInfo{
			Address:    serverInfo.Address,
			Port:       serverInfo.Port,
			SigningKey: []byte("invalid"),
		}
		accessToken, err := GenerateExtensionToken(extension, invalidServerInfo)
		require.NoError(t, err)

		ctx := azdext.WithAccessToken(t.Context(), accessToken)
		client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
		require.NoError(t, err)
		defer client.Close()

		stream, err := client.Events().EventStream(ctx)
		if err != nil {
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.Unauthenticated, st.Code())
		} else {
			// For bidi streams, the auth error may surface on Recv rather than stream creation.
			_, recvErr := stream.Recv()
			require.Error(t, recvErr)
			st, ok := status.FromError(recvErr)
			require.True(t, ok)
			require.Equal(t, codes.Unauthenticated, st.Code())
		}
	})
}

func Test_mapHostError(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		wantNil          bool
		wantContain      string
		wantNotContain   string
		wantSameInstance bool
		wantGrpcCode     codes.Code
		wantAuthReason   string
		wantSuggestion   string
		wantLinks        int
		wantLinkURL      string
		wantLinkTitle    string
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
			name: "error with suggestion preserves suggestion structurally, not in message",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("authentication failed"),
				Suggestion: "run `azd auth login` to acquire a new token.",
			},
			wantContain:    "authentication failed",
			wantNotContain: "azd auth login",
			wantSuggestion: "run `azd auth login` to acquire a new token.",
		},
		{
			name: "wrapped error with suggestion preserves suggestion structurally, not in message",
			err: fmt.Errorf("failed to prompt: %w", &internal.ErrorWithSuggestion{
				Err:        errors.New("token expired"),
				Suggestion: "login expired, run `azd auth login` to acquire a new token.",
			}),
			wantContain:    "token expired",
			wantNotContain: "azd auth login",
			wantSuggestion: "login expired, run `azd auth login` to acquire a new token.",
		},
		{
			name:           "ErrNoCurrentUser returns Unauthenticated",
			err:            auth.ErrNoCurrentUser,
			wantContain:    "not logged in",
			wantGrpcCode:   codes.Unauthenticated,
			wantAuthReason: azdext.AuthErrorReasonNotLoggedIn,
		},
		{
			name:           "wrapped ErrNoCurrentUser returns Unauthenticated",
			err:            fmt.Errorf("failed to list subscriptions: %w", auth.ErrNoCurrentUser),
			wantContain:    "not logged in",
			wantGrpcCode:   codes.Unauthenticated,
			wantAuthReason: azdext.AuthErrorReasonNotLoggedIn,
		},
		{
			name: "ReLoginRequiredError with suggestion returns Unauthenticated",
			err: &internal.ErrorWithSuggestion{
				Err:        &auth.ReLoginRequiredError{},
				Message:    "Re-authentication required.",
				Suggestion: "login expired, run `azd auth login` to acquire a new token.",
			},
			wantContain:    "Re-authentication required.",
			wantNotContain: "azd auth login",
			wantGrpcCode:   codes.Unauthenticated,
			wantAuthReason: azdext.AuthErrorReasonLoginRequired,
			wantSuggestion: "login expired, run `azd auth login` to acquire a new token.",
		},
		{
			name: "TokenProtectionBlockedError with suggestion returns Unauthenticated",
			err: &internal.ErrorWithSuggestion{
				Err: &auth.AuthFailedError{
					Parsed: &auth.AadErrorResponse{
						Error:      "invalid_grant",
						ErrorCodes: []int{530084},
					},
				},
				Message:    "A Conditional Access token protection policy blocked this token request.",
				Suggestion: "Contact your IT administrator or request a policy exception.",
				Links: []errorhandler.ErrorLink{{
					URL:   "https://aka.ms/TokenProtectionFAQ#troubleshooting",
					Title: "Token protection FAQ",
				}},
			},
			wantContain:    "Conditional Access",
			wantNotContain: "policy exception",
			wantGrpcCode:   codes.Unauthenticated,
			wantAuthReason: "AADSTS530084",
			wantSuggestion: "Contact your IT administrator or request a policy exception.",
			wantLinks:      1,
			wantLinkURL:    "https://aka.ms/TokenProtectionFAQ#troubleshooting",
			wantLinkTitle:  "Token protection FAQ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapHostError(tt.err)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Contains(t, result.Error(), tt.wantContain)
			if tt.wantNotContain != "" {
				require.NotContains(t, result.Error(), tt.wantNotContain,
					"suggestion text must not be concatenated into status.Message")
			}
			if tt.wantSameInstance {
				require.Same(t, tt.err, result, "expected error to be returned unchanged (same instance)")
			}
			if tt.wantGrpcCode != 0 {
				st, ok := status.FromError(result)
				require.True(t, ok, "expected gRPC status error")
				require.Equal(t, tt.wantGrpcCode, st.Code())
				if tt.wantAuthReason != "" {
					info := requireAuthErrorInfo(t, st)
					require.Equal(t, azdext.AuthErrorDomain, info.Domain)
					require.Equal(t, tt.wantAuthReason, info.Reason)
				}
			}
			if tt.wantSuggestion != "" {
				st, ok := status.FromError(result)
				require.True(t, ok, "expected gRPC status error")
				actionable := azdext.ActionableErrorDetailFromStatus(st)
				require.NotNil(t, actionable, "expected ActionableErrorDetail")
				require.Equal(t, tt.wantSuggestion, actionable.GetSuggestion())
				require.Len(t, actionable.GetLinks(), tt.wantLinks)
				if tt.wantLinkURL != "" {
					require.Equal(t, tt.wantLinkURL, actionable.GetLinks()[0].GetUrl())
					require.Equal(t, tt.wantLinkTitle, actionable.GetLinks()[0].GetTitle())
				}
			}
		})
	}
}

func requireAuthErrorInfo(t *testing.T, st *status.Status) *errdetails.ErrorInfo {
	t.Helper()

	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}

	require.FailNow(t, "expected ErrorInfo detail")
	return nil
}

func TestAuthenticatedStream_Context(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(t.Context(), struct{ key string }{key: "test"}, "value")

	stream := &authenticatedStream{
		ctx: ctx,
	}

	got := stream.Context()
	require.Equal(t, ctx, got)
	require.Equal(t, "value", got.Value(struct{ key string }{key: "test"}))
}

func TestMapHostError_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, mapHostError(nil))
}

func TestMapHostError_PlainError(t *testing.T) {
	t.Parallel()
	err := errors.New("something failed")
	wrapped := mapHostError(err)
	require.Equal(t, err, wrapped)
}

func TestMapHostError_WithSuggestion(t *testing.T) {
	t.Parallel()
	inner := errors.New("login required")
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "run azd auth login",
	}

	wrapped := mapHostError(err)
	require.Contains(t, wrapped.Error(), "login required")
	require.NotContains(t, wrapped.Error(), "run azd auth login",
		"suggestion text must not be concatenated into status.Message")

	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	actionable := azdext.ActionableErrorDetailFromStatus(st)
	require.NotNil(t, actionable)
	require.Equal(t, "run azd auth login", actionable.GetSuggestion())
}

func TestMapHostError_AuthError(t *testing.T) {
	t.Parallel()
	err := auth.ErrNoCurrentUser
	wrapped := mapHostError(err)

	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestMapHostError_ReLoginRequired(t *testing.T) {
	t.Parallel()
	// ReLoginRequiredError has unexported fields; use a simple error wrapping
	err := fmt.Errorf("re-login: %w", &auth.ReLoginRequiredError{})

	wrapped := mapHostError(err)
	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestMapHostError_TokenProtectionBlocked(t *testing.T) {
	t.Parallel()
	authFailed := &auth.AuthFailedError{
		Parsed: &auth.AadErrorResponse{
			Error:      "invalid_grant",
			ErrorCodes: []int{530084},
		},
	}
	// In production the wrapper is built by newActionableAuthError; mirror that shape here so
	// mapHostError classifies the wrapped *AuthFailedError as an auth interaction.
	err := fmt.Errorf("token protection: %w", &internal.ErrorWithSuggestion{
		Err:        authFailed,
		Suggestion: "Contact your IT administrator or request a policy exception.",
	})

	wrapped := mapHostError(err)
	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
	info := requireAuthErrorInfo(t, st)
	require.Equal(t, azdext.AuthErrorDomain, info.Domain)
	require.Equal(t, "AADSTS530084", info.Reason)
	actionable := azdext.ActionableErrorDetailFromStatus(st)
	require.NotNil(t, actionable)
	require.Equal(t, "Contact your IT administrator or request a policy exception.", actionable.GetSuggestion())
}

func TestMapHostError_AuthErrorWithSuggestion(t *testing.T) {
	t.Parallel()
	inner := auth.ErrNoCurrentUser
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "run azd auth login",
	}

	wrapped := mapHostError(err)
	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
	require.NotContains(t, st.Message(), "run azd auth login",
		"suggestion text must not be concatenated into status.Message")
	actionable := azdext.ActionableErrorDetailFromStatus(st)
	require.NotNil(t, actionable)
	require.Equal(t, "run azd auth login", actionable.GetSuggestion())
}

func TestGenerateSigningKey(t *testing.T) {
	t.Parallel()
	key, err := generateSigningKey()
	require.NoError(t, err)
	require.Len(t, key, 32)

	// Keys should be unique
	key2, err := generateSigningKey()
	require.NoError(t, err)
	require.NotEqual(t, key, key2)
}

func TestServerStop_NotRunning(t *testing.T) {
	t.Parallel()
	s := &Server{}
	err := s.Stop()
	require.Error(t, err)
	require.Contains(t, err.Error(), "server is not running")
}

func TestErrorWrappingInterceptor(t *testing.T) {
	t.Parallel()
	s := &Server{}
	interceptor := s.errorWrappingInterceptor()

	// Test with no error
	resp, err := interceptor(t.Context(), nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", resp)

	// Test with auth error
	resp, err = interceptor(t.Context(), nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return nil, auth.ErrNoCurrentUser
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
	require.Nil(t, resp)
}

func TestErrorWrappingStreamInterceptor(t *testing.T) {
	t.Parallel()
	s := &Server{}
	interceptor := s.errorWrappingStreamInterceptor()

	// Test with no error
	err := interceptor(nil, nil, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		return nil
	})
	require.NoError(t, err)

	// Test with auth error
	err = interceptor(nil, nil, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		return auth.ErrNoCurrentUser
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestValidateAuthToken_MissingMetadata(t *testing.T) {
	t.Parallel()
	s := &Server{}
	info := &ServerInfo{SigningKey: []byte("testtesttesttesttesttesttesttest1")}

	_, err := s.validateAuthToken(t.Context(), info)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestValidateAuthToken_MissingToken(t *testing.T) {
	t.Parallel()
	s := &Server{}
	info := &ServerInfo{SigningKey: []byte("testtesttesttesttesttesttesttest1")}

	md := metadata.Pairs("content-type", "application/grpc")
	ctx := metadata.NewIncomingContext(t.Context(), md)

	_, err := s.validateAuthToken(ctx, info)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestValidateAuthToken_InvalidToken(t *testing.T) {
	t.Parallel()
	s := &Server{}
	info := &ServerInfo{SigningKey: []byte("testtesttesttesttesttesttesttest1")}

	md := metadata.Pairs("authorization", "not-a-valid-jwt")
	ctx := metadata.NewIncomingContext(t.Context(), md)

	_, err := s.validateAuthToken(ctx, info)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestNewServer(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, s)
	assert.Nil(t, s.grpcServer, "grpcServer should be nil before Start")
}

func TestServerInfo(t *testing.T) {
	t.Parallel()
	info := ServerInfo{
		Address:    "127.0.0.1:8080",
		Port:       8080,
		SigningKey: []byte("test-key"),
	}
	require.Equal(t, "127.0.0.1:8080", info.Address)
	require.Equal(t, 8080, info.Port)
	require.Equal(t, []byte("test-key"), info.SigningKey)
}

// mockPromptService implements prompt.PromptService for testing.
type mockPromptService struct {
	promptSubscriptionFn func(ctx context.Context, opts *prompt.SelectOptions) (*account.Subscription, error)
	promptLocationFn     func(
		ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
	) (*account.Location, error)
	promptResourceGroupFn func(
		ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
	) (*azapi.ResourceGroup, error)
	promptSubscriptionResourceFn func(
		ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
	) (*azapi.ResourceExtended, error)
	promptResourceGroupResourceFn func(
		ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
	) (*azapi.ResourceExtended, error)
}

func TestPromptService_Select_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Select(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_Select_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Select(t.Context(), &azdext.SelectRequest{})
	require.Error(t, err)
}

func TestPromptService_Select_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "choose:",
			SelectedIndex: proto.Int32(2),
			Choices: []*azdext.SelectChoice{
				{Value: "a", Label: "A"},
				{Value: "b", Label: "B"},
				{Value: "c", Label: "C"},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.Equal(t, int32(2), *resp.Value)
}

func TestPromptService_Select_NoPrompt_NoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "choose:",
		},
	})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "choose:")
}

func TestPromptService_PromptSubscription_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionFn: func(ctx context.Context, opts *prompt.SelectOptions) (*account.Subscription, error) {
			return nil, errors.New("no subscriptions")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptSubscription(t.Context(), &azdext.PromptSubscriptionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no subscriptions")
}

func TestPromptService_PromptLocation_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptLocationFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
		) (*account.Location, error) {
			return nil, errors.New("location error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

func TestPromptService_PromptResourceGroup_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
		) (*azapi.ResourceGroup, error) {
			return nil, errors.New("rg error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

func TestPromptService_PromptSubscriptionResource_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptSubscriptionResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return nil, errors.New("resource error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}

func TestPromptService_PromptResourceGroupResource_Error(t *testing.T) {
	t.Parallel()
	mock := &mockPromptService{
		promptResourceGroupResourceFn: func(
			ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
		) (*azapi.ResourceExtended, error) {
			return nil, errors.New("rg resource error")
		},
	}
	svc := newTestPromptService(mock, false)
	_, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				TenantId:       "t-1",
			},
		},
	})
	require.Error(t, err)
}
