// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package runcontext

import (
	"testing"

	"github.com/azure/azure-dev/test/ostest"
	"github.com/stretchr/testify/require"
)

func TestIsRunningInCloudShellScenarios(t *testing.T) {
	t.Run("returns true when AZD_IN_CLOUDSHELL is set to true-ish string", func(t *testing.T) {
		t.Setenv(AzdInCloudShellEnvVar, "1")
		require.True(t, IsRunningInCloudShell())
	})

	t.Run("returns false when AZD_IN_CLOUDSHELL is set to false-ish string", func(t *testing.T) {
		t.Setenv(AzdInCloudShellEnvVar, "0")
		require.False(t, IsRunningInCloudShell())
	})

	t.Run("returns false when AZD_IN_CLOUDSHELL is not set", func(t *testing.T) {
		ostest.Unsetenv(t, AzdInCloudShellEnvVar)
		require.False(t, IsRunningInCloudShell())
	})

}
