// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build oneauth && windows

package oneauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	require.NoError(t, start("clientID"))
}

func TestSupported(t *testing.T) {
	require.True(t, Supported)
}
