// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
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

	// Empty messages are not progress messages.
	require.False(t, env.IsProgressMessage(msg))

	// Empty messages have no progress text.
	require.Empty(t, env.GetProgressMessage(msg))

	require.NotNil(t, env.CreateProgressMessage("id", "msg"))
}

func TestEventMessageEnvelope_HandlerOutputProgress(t *testing.T) {
	env := NewEventMessageEnvelope()
	msg := env.CreateProgressMessage("request-id", "handler output")

	require.Equal(t, "request-id", env.GetRequestId(t.Context(), msg))
	require.Equal(t, "handler output", env.GetProgressMessage(msg))
	require.True(t, env.IsProgressMessage(msg))
	require.Equal(t, "handler output", msg.GetHandlerOutput().GetOutput())
}

func TestEventMessageEnvelope_GetRequestId_UsesConfiguredExtensionId(t *testing.T) {
	env := newEventMessageEnvelope("configured-ext")

	projectMsg := &EventMessage{
		MessageType: &EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &InvokeProjectHandler{
				EventName: "predeploy",
			},
		},
	}
	serviceMsg := &EventMessage{
		MessageType: &EventMessage_InvokeServiceHandler{
			InvokeServiceHandler: &InvokeServiceHandler{
				EventName: "predeploy",
				Service:   &ServiceConfig{Name: "api"},
			},
		},
	}

	require.Equal(t, "configured-ext.predeploy", env.GetRequestId(t.Context(), projectMsg))
	require.Equal(t, "configured-ext.api.predeploy", env.GetRequestId(t.Context(), serviceMsg))
}

func TestEventMessageEnvelope_GetRequestId_PrefersClaims(t *testing.T) {
	env := newEventMessageEnvelope("configured-ext")
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "claimed-ext"},
	})

	msg := &EventMessage{
		MessageType: &EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &InvokeProjectHandler{
				EventName: "predeploy",
			},
		},
	}

	require.Equal(t, "claimed-ext.predeploy", env.GetRequestId(ctx, msg))
}
