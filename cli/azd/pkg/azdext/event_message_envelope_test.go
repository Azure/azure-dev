// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

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
