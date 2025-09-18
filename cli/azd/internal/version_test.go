// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionInfo(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "0.1.0-beta.2 (commit 13ec2b11aa755b11640fa16b8664cb8741d5d300)"
	info := VersionInfo()
	require.Equal(t, "0.1.0-beta.2", info.Version.String())
	require.Equal(t, "13ec2b11aa755b11640fa16b8664cb8741d5d300", info.Commit)

	Version = "invalid"
	require.Panics(t, func() { _ = VersionInfo() })

	Version = ""
	require.Panics(t, func() { _ = VersionInfo() })
}
