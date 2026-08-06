// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one difference between create and update, and the only thing stopping a
// create from silently publishing version 2 of someone else's dataset.
func TestCheckAssetExistence(t *testing.T) {
	assert.NoError(t, checkAssetExistence("create", "dataset", "x", false))
	assert.NoError(t, checkAssetExistence("update", "dataset", "x", true))

	err := checkAssetExistence("create", "dataset", "x", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update", "the error has to name the verb that works")

	err = checkAssetExistence("update", "dataset", "x", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create")
}
