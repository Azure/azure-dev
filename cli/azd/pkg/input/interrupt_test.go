// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPushInterruptHandler_LIFO(t *testing.T) {
	require.Nil(t, currentInterruptHandler())

	first := func() bool { return true }
	pop1 := PushInterruptHandler(first)

	require.NotNil(t, currentInterruptHandler())

	second := func() bool { return true }
	pop2 := PushInterruptHandler(second)

	// Top-of-stack should be `second` (most recently pushed).
	cur := currentInterruptHandler()
	require.NotNil(t, cur)

	pop2()
	// After popping `second`, current should be `first` again.
	require.NotNil(t, currentInterruptHandler())

	pop1()
	require.Nil(t, currentInterruptHandler())
}

func TestTryStartInterruptHandler_PreventsConcurrent(t *testing.T) {
	require.True(t, tryStartInterruptHandler())
	defer finishInterruptHandler()

	// While the first handler is "running", the second start should be
	// rejected so additional Ctrl+C signals are ignored.
	require.False(t, tryStartInterruptHandler())
}
