// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestEventMessageEnvelope_NoOps(t *testing.T) {
	env := NewEventMessageEnvelope()
	msg := &EventMessage{}

	// SetRequestId is a no-op
	env.SetRequestId(t.Context(), msg, "ignored")

	// GetError always returns nil
	require.Nil(t, env.GetError(msg))

	// SetError is a no-op
	env.SetError(msg, &LocalError{Message: "ignored"})

	// IsProgressMessage always false
	require.False(t, env.IsProgressMessage(msg))

	// GetProgressMessage always empty
	require.Empty(t, env.GetProgressMessage(msg))

	// CreateProgressMessage always nil
	require.Nil(t, env.CreateProgressMessage("id", "msg"))

	require.Nil(t, env.GetInnerMessage(nil))
	require.Nil(t, env.CreateCancellationMessage(t.Context(), nil, nil))
}

func TestEventMessageEnvelope_CancellationMessage(t *testing.T) {
	env := NewEventMessageEnvelope("test.extension")
	request := &EventMessage{
		MessageType: &EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &InvokeProjectHandler{EventName: "prepackage"},
		},
	}

	cancelMsg := env.CreateCancellationMessage(t.Context(), request, context.Canceled)
	require.NotNil(t, cancelMsg)
	require.True(t, env.IsCancellationMessage(t.Context(), cancelMsg))
	require.Equal(t, "test.extension.prepackage", env.GetRequestId(t.Context(), cancelMsg))
	require.ErrorIs(t, UnwrapError(cancelMsg.GetProjectHandlerStatus().GetError()), context.Canceled)
}

func TestEventMessageEnvelope_UsesConfiguredExtensionIdWithoutClaims(t *testing.T) {
	env := NewEventMessageEnvelope("test.extension")
	msg := &EventMessage{
		MessageType: &EventMessage_InvokeServiceHandler{
			InvokeServiceHandler: &InvokeServiceHandler{
				EventName: "predeploy",
				Service:   &ServiceConfig{Name: "api"},
			},
		},
	}

	require.Equal(t, "test.extension.api.predeploy", env.GetRequestId(t.Context(), msg))
}

func TestEventMessageEnvelope_UsesConfiguredExtensionIdForEmptyClaims(t *testing.T) {
	env := NewEventMessageEnvelope("test.extension")
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{})
	msg := &EventMessage{
		MessageType: &EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &InvokeProjectHandler{EventName: "prepackage"},
		},
	}

	require.Equal(t, "test.extension.prepackage", env.GetRequestId(ctx, msg))
}
