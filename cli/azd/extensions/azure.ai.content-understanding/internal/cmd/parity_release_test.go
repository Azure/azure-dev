//go:build parityrelease

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParityReleaseReadiness(t *testing.T) {
	inventory := loadParityInventory(t)
	require.NoError(t, validateParityInventory(inventory))
	require.NoError(t, parityReadiness(inventory))
}
