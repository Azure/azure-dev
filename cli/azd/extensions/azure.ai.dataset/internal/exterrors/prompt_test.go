// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A prompt that could not be drawn is not the same as one that was answered.
//
// With stdout redirected there is no console to draw on, and the host reports
// that as `rpc error: code = Unknown desc = The handle is invalid` -- a
// transport detail the reader can do nothing with. It is the caller's cue to
// pass --force, so it has to be told apart from a decline or a refusal.
func TestIsPromptUnavailable(t *testing.T) {
	assert.True(t, IsPromptUnavailable(
		status.Error(codes.Unknown, "The handle is invalid.")),
		"no console to ask on")

	assert.True(t, IsPromptUnavailable(errors.New("connection closed")),
		"a prompt that never reached the host was never asked either")

	assert.False(t, IsPromptUnavailable(context.Canceled),
		"the caller answered by cancelling")

	assert.False(t, IsPromptUnavailable(status.Error(codes.Canceled, "cancelled")))

	assert.False(t, IsPromptUnavailable(status.Error(codes.Unauthenticated, "login expired")),
		"the question was reached and refused, and re-auth is the advice that helps")

	assert.False(t, IsPromptUnavailable(nil))
}
