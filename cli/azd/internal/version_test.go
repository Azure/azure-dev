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

func TestGetVersionSpec(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "0.1.0-beta.2 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)"
	vSpec := GetVersionSpec()
	require.Equal(t, "0.1.0-beta.2", vSpec.Azd.Version)
	require.Equal(t, "13ec2b11aa755b11640fa16b8664cb8741d5d300", vSpec.Azd.Commit)

	Version = "invalid"
	vSpec = GetVersionSpec()
	require.Equal(t, "unknown", vSpec.Azd.Version)
	require.Equal(t, "unknown", vSpec.Azd.Commit)

	Version = ""
	vSpec = GetVersionSpec()
	require.Equal(t, "unknown", vSpec.Azd.Version)
	require.Equal(t, "unknown", vSpec.Azd.Commit)
}
