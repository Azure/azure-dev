// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPushInterruptHandler_LIFO(t *testing.T) {
	require.Nil(t, currentInterruptHandler())

	firstCalls := 0
	first := func() bool {
		firstCalls++
		return true
	}
	pop1 := PushInterruptHandler(first)

	cur := currentInterruptHandler()
	require.NotNil(t, cur)
	require.True(t, cur())
	require.Equal(t, 1, firstCalls)

	secondCalls := 0
	second := func() bool {
		secondCalls++
		return true
	}
	pop2 := PushInterruptHandler(second)

	// Top-of-stack should be `second` (most recently pushed).
	cur = currentInterruptHandler()
	require.NotNil(t, cur)
	require.True(t, cur())
	require.Equal(t, 1, firstCalls, "pushing second must not invoke first")
	require.Equal(t, 1, secondCalls)

	pop2()
	// After popping `second`, current should be `first` again.
	cur = currentInterruptHandler()
	require.NotNil(t, cur)
	require.True(t, cur())
	require.Equal(t, 2, firstCalls)
	require.Equal(t, 1, secondCalls, "popping second must not re-invoke it")

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
