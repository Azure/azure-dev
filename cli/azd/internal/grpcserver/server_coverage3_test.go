// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

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

func TestWrapErrorWithSuggestion_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, wrapErrorWithSuggestion(nil))
}

func TestWrapErrorWithSuggestion_PlainError(t *testing.T) {
	t.Parallel()
	err := errors.New("something failed")
	wrapped := wrapErrorWithSuggestion(err)
	require.Equal(t, err, wrapped)
}

func TestWrapErrorWithSuggestion_WithSuggestion(t *testing.T) {
	t.Parallel()
	inner := errors.New("login required")
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "run azd auth login",
	}

	wrapped := wrapErrorWithSuggestion(err)
	require.Contains(t, wrapped.Error(), "run azd auth login")
}

func TestWrapErrorWithSuggestion_AuthError(t *testing.T) {
	t.Parallel()
	err := auth.ErrNoCurrentUser
	wrapped := wrapErrorWithSuggestion(err)

	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestWrapErrorWithSuggestion_ReLoginRequired(t *testing.T) {
	t.Parallel()
	// ReLoginRequiredError has unexported fields; use a simple error wrapping
	err := fmt.Errorf("re-login: %w", &auth.ReLoginRequiredError{})

	wrapped := wrapErrorWithSuggestion(err)
	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
}

func TestWrapErrorWithSuggestion_AuthErrorWithSuggestion(t *testing.T) {
	t.Parallel()
	inner := auth.ErrNoCurrentUser
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "run azd auth login",
	}

	wrapped := wrapErrorWithSuggestion(err)
	st, ok := status.FromError(wrapped)
	require.True(t, ok)
	require.Equal(t, codes.Unauthenticated, st.Code())
	require.Contains(t, st.Message(), "run azd auth login")
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
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, s)
	assert.Nil(t, s.grpcServer, "grpcServer should be nil before Start")
}

func TestServerInfo(t *testing.T) {
	t.Parallel()
	info := ServerInfo{
		Address:    "127.0.0.1:8080",
		Port:       8080,
		SigningKey:  []byte("test-key"),
	}
	require.Equal(t, "127.0.0.1:8080", info.Address)
	require.Equal(t, 8080, info.Port)
	require.Equal(t, []byte("test-key"), info.SigningKey)
}
