package internal

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Scenario tests
func TestUserAgentStringScenarios(t *testing.T) {
	version := VersionInfo().Version.String()
	require.NotEmpty(t, version)

	azDevIdentifier := fmt.Sprintf("azdev/%s %s", version, runtimeInfo())

	t.Run("default", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "false")
		t.Setenv(AzdUserAgentEnvVar, "")
		require.Equal(t, azDevIdentifier, UserAgent())
	})

	t.Run("withUserAgent", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "false")
		t.Setenv(AzdUserAgentEnvVar, "dev_user_agent")
		require.Equal(t, fmt.Sprintf("%s dev_user_agent", azDevIdentifier), UserAgent())
	})

	t.Run("onGitHubActions", func(t *testing.T) {
		t.Setenv("GITHUB_ACTIONS", "true")
		t.Setenv(AzdUserAgentEnvVar, "")
		require.Equal(t, fmt.Sprintf("%s GhActions", azDevIdentifier), UserAgent())
	})

	t.Run("withEverything", func(t *testing.T) {
		t.Setenv(AzdUserAgentEnvVar, "dev_user_agent")
		t.Setenv("GITHUB_ACTIONS", "true")
		require.Equal(
			t,
			fmt.Sprintf("%s dev_user_agent GhActions", azDevIdentifier),
			UserAgent(),
		)
	})
}
