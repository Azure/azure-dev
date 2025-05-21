// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDestroyOptions(t *testing.T) {
	options := NewDestroyOptions(true, true, true)

	require.True(t, options.Force())
	require.True(t, options.Purge())
	require.True(t, options.NoWait())

	options = NewDestroyOptions(false, false, false)

	require.False(t, options.Force())
	require.False(t, options.Purge())
	require.False(t, options.NoWait())
}