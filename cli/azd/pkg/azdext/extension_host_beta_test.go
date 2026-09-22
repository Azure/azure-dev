// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExtensionHost_BetaPreviewRegistration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	manager := &MockServiceTargetRegistrar{}
	receiving := make(chan struct{})
	manager.On("Ready", mock.Anything).Run(func(mock.Arguments) { <-receiving }).Return(nil).Once()
	manager.On("Receive", mock.Anything).Run(func(mock.Arguments) {
		close(receiving)
		<-ctx.Done()
	}).Return(nil).Once()
	manager.On("Close").Return(nil).Once()
	manager.On("Register", mock.Anything, mock.Anything, "custom").
		Run(func(mock.Arguments) { cancel() }).Return(nil).Once()
	var factoryCalls atomic.Int32
	host := NewExtensionHost(newTestAzdClient()).WithBetaServiceTargetPreview("custom", func() ServiceTargetProvider {
		factoryCalls.Add(1)
		return &mockServiceTargetPreviewProvider{}
	})
	host.betaServiceTargetManager = manager
	require.Empty(t, host.ServiceTargets())
	registrations := host.BetaServiceTargets()
	require.Len(t, registrations, 1)
	registrations[0].Host = "changed"
	require.Equal(t, "custom", host.BetaServiceTargets()[0].Host)
	require.NoError(t, host.Run(ctx))
	require.Zero(t, factoryCalls.Load())
	manager.AssertExpectations(t)
}

func TestExtensionHost_BetaPreviewRegistrationValidation(t *testing.T) {
	t.Parallel()
	factory := func() ServiceTargetProvider {
		t.Fatal("invalid registrations must not construct providers")
		return nil
	}
	host := NewExtensionHost(nil).
		WithServiceTarget("custom", factory).
		WithBetaServiceTargetPreview("custom", factory)
	require.EqualError(t, host.Run(t.Context()),
		"service target 'custom' cannot be registered on both stable and beta channels")
	host = NewExtensionHost(nil).WithBetaServiceTargetPreview("custom", nil)
	require.EqualError(t, host.Run(t.Context()), "beta service target provider for host 'custom' is nil")
}
