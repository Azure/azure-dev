// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TempDir resolves platform aliases in a test-owned temporary root, not in paths under test.
func TempDir(t testing.TB) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return directory
}
