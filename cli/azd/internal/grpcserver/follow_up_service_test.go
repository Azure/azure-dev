// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"sync"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFollowUpManagerLifecycle(t *testing.T) {
	manager := NewFollowUpManager()
	invocationID := manager.Begin("test.extension", "postdeploy")

	require.NoError(t, manager.Set(invocationID, "test.extension", "next"))

	text, ok := manager.Commit(invocationID)
	require.True(t, ok)
	require.Equal(t, "next", text)

	require.ErrorIs(t,
		manager.Set(invocationID, "test.extension", "late"),
		errFollowUpInvocationNotFound,
	)
}

func TestFollowUpManagerRejectsInvalidContributions(t *testing.T) {
	manager := NewFollowUpManager()

	tests := []struct {
		name      string
		eventName string
		extension string
		want      error
	}{
		{
			name:      "non post event",
			eventName: "predeploy",
			extension: "test.extension",
			want:      errFollowUpNotAllowed,
		},
		{
			name:      "wrong extension",
			eventName: "postdeploy",
			extension: "other.extension",
			want:      errFollowUpExtensionMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocationID := manager.Begin("test.extension", test.eventName)
			require.ErrorIs(t,
				manager.Set(invocationID, test.extension, "next"),
				test.want,
			)
			manager.Discard(invocationID)
		})
	}
}

func TestFollowUpManagerClearAndDiscard(t *testing.T) {
	manager := NewFollowUpManager()

	invocationID := manager.Begin("test.extension", "postdeploy")
	require.NoError(t, manager.Set(invocationID, "test.extension", "next"))
	require.NoError(t, manager.Set(invocationID, "test.extension", ""))

	text, ok := manager.Commit(invocationID)
	require.True(t, ok)
	require.Empty(t, text)

	invocationID = manager.Begin("test.extension", "postdeploy")
	require.NoError(t, manager.Set(invocationID, "test.extension", "discarded"))
	manager.Discard(invocationID)

	text, ok = manager.Commit(invocationID)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestFollowUpManagerSerializesSetAndFinish(t *testing.T) {
	manager := NewFollowUpManager()
	invocationID := manager.Begin("test.extension", "postdeploy")

	var wait sync.WaitGroup
	for range 20 {
		wait.Go(func() {
			_ = manager.Set(invocationID, "test.extension", "next")
		})
	}
	wait.Go(func() {
		manager.Discard(invocationID)
	})
	wait.Wait()

	_, ok := manager.Commit(invocationID)
	require.False(t, ok)
}

func TestFollowUpServiceSetFollowUp(t *testing.T) {
	manager := NewFollowUpManager()
	service := NewFollowUpService(manager)
	invocationID := manager.Begin("test.extension", "postdeploy")

	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test.extension"},
	})
	_, err := service.SetFollowUp(ctx, &v1beta.SetFollowUpRequest{
		InvocationId: invocationID,
		Text:         "next",
	})
	require.NoError(t, err)

	text, ok := manager.Commit(invocationID)
	require.True(t, ok)
	require.Equal(t, "next", text)
}

func TestFollowUpServiceSetFollowUpRejectsInvalidRequests(t *testing.T) {
	manager := NewFollowUpManager()
	service := NewFollowUpService(manager)

	_, err := service.SetFollowUp(t.Context(), nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = service.SetFollowUp(t.Context(), &v1beta.SetFollowUpRequest{
		InvocationId: "missing",
	})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	invocationID := manager.Begin("test.extension", "postdeploy")
	otherCtx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "other.extension"},
	})
	_, err = service.SetFollowUp(otherCtx, &v1beta.SetFollowUpRequest{
		InvocationId: invocationID,
		Text:         "next",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	nonPostID := manager.Begin("test.extension", "predeploy")
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test.extension"},
	})
	_, err = service.SetFollowUp(ctx, &v1beta.SetFollowUpRequest{
		InvocationId: nonPostID,
		Text:         "next",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	manager.Discard(invocationID)
	manager.Discard(nonPostID)
}

func TestFollowUpServiceSetFollowUpRejectsClosedInvocation(t *testing.T) {
	manager := NewFollowUpManager()
	service := NewFollowUpService(manager)
	invocationID := manager.Begin("test.extension", "postdeploy")
	manager.Discard(invocationID)

	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test.extension"},
	})
	_, err := service.SetFollowUp(ctx, &v1beta.SetFollowUpRequest{
		InvocationId: invocationID,
		Text:         "late",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
