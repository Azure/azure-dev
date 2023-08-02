package runcontext

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const cAzdInCloudShellEnvVar = "AZD_IN_CLOUDSHELL"

func TestIsRunningInCloudShellScenarios(t *testing.T) {
	t.Run("returns true when AZD_IN_CLOUDSHELL is set to true-ish string", func(t *testing.T) {
		t.Setenv(cAzdInCloudShellEnvVar, "1")
		require.True(t, IsRunningInCloudShell())
	})

	t.Run("returns false when AZD_IN_CLOUDSHELL is set to false-ish string", func(t *testing.T) {
		t.Setenv(cAzdInCloudShellEnvVar, "0")
		require.False(t, IsRunningInCloudShell())
	})

	t.Run("returns false when AZD_IN_CLOUDSHELL is not set", func(t *testing.T) {
		// Ensure that AZD_IN_CLOUDSHELL is not set otherwise the test is not
		// accurate
		_, envIsSet := os.LookupEnv(cAzdInCloudShellEnvVar)
		require.False(t, envIsSet)

		require.False(t, IsRunningInCloudShell())
	})

}
