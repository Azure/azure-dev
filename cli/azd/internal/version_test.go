// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetVersionNumber(t *testing.T) {
	require.Equal(t, "0.0.0-dev.0", GetVersionNumber())

	orig := Version
	Version = "invalid"
	defer func() { Version = orig }()

	require.Equal(t, "unknown", GetVersionNumber())

	Version = ""
	require.Equal(t, "unknown", GetVersionNumber())
}
