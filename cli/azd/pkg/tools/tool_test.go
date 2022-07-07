// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_toolInPath(t *testing.T) {
	t.Run("Missing", func(t *testing.T) {
		has, err := toolInPath("somethingThatNeverExists")
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("Installed", func(t *testing.T) {
		// 'az' is a prerequisite to even develop in this package right now.
		has, err := toolInPath("az")
		require.NoError(t, err)
		require.True(t, has)
	})
}
