// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build oneauth && windows

package oneauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartShutdown(t *testing.T) {
	fakeClientID := "7922c055-2cb8-4450-9669-c4952562f2b9"
	require.NoError(t, start(fakeClientID))
	Shutdown()
}

func TestSupported(t *testing.T) {
	require.True(t, Supported)
}
