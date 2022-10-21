package internal

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAzDevCliIdentifier(t *testing.T) {
	version := GetVersionNumber()
	require.NotEmpty(t, version)

	require.Equal(t, fmt.Sprintf("%s/%s %s", azDevProductIdentifierKey, version, getPlatformInfo()), getAzDevCliIdentifier())
}

func TestUserSpecifiedAgentIdentifier(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  string
		expect string
	}{
		{"custom", "MyAgent/1.0.0", "MyAgent/1.0.0"},
		{"empty", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(AzdUserAgentEnvVar, test.value)
			require.Equal(t, test.expect, GetCallerUserAgent())
		})
	}
}

func TestGithubActionIdentifier(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  string
		expect string
	}{
		{"empty", "", ""},
		{"set", "true", "GhActions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(githubActionsEnvironmentVariableName, test.value)
			require.Equal(t, test.expect, getGithubActionsIdentifier())
		})
	}
}

func TestFormatTemplate(t *testing.T) {
	require.Equal(t, "", formatTemplateIdentifier(""))
	require.Equal(
		t,
		fmt.Sprintf("%s/todo-python-mongo", templateProductIdentifierKey),
		formatTemplateIdentifier("todo-python-mongo"),
	)
	require.Equal(
		t,
		fmt.Sprintf("%s/todo-csharp-sql@0.0.1-beta", templateProductIdentifierKey),
		formatTemplateIdentifier("todo-csharp-sql@0.0.1-beta"),
	)
}

// Scenario tests
func TestUserAgentStringScenarios(t *testing.T) {
	version := GetVersionNumber()
	require.NotEmpty(t, version)

	azDevIdentifier := fmt.Sprintf("azdev/%s %s", version, getPlatformInfo())

	t.Run("default", func(t *testing.T) {
		require.Equal(t, azDevIdentifier, MakeUserAgentString(""))
	})

	t.Run("withUserAgent", func(t *testing.T) {
		t.Setenv(AzdUserAgentEnvVar, "dev_user_agent")
		require.Equal(t, fmt.Sprintf("%s dev_user_agent", azDevIdentifier), MakeUserAgentString(""))
	})

	t.Run("onGitHubActions", func(t *testing.T) {
		t.Setenv(githubActionsEnvironmentVariableName, "true")
		require.Equal(t, fmt.Sprintf("%s GhActions", azDevIdentifier), MakeUserAgentString(""))
	})

	t.Run("withTemplate", func(t *testing.T) {
		require.Equal(t, fmt.Sprintf("%s azdtempl/template@0.0.1", azDevIdentifier), MakeUserAgentString("template@0.0.1"))
	})

	t.Run("withEverything", func(t *testing.T) {
		t.Setenv(AzdUserAgentEnvVar, "dev_user_agent")
		t.Setenv(githubActionsEnvironmentVariableName, "true")
		require.Equal(
			t,
			fmt.Sprintf("%s dev_user_agent azdtempl/template@0.0.1 GhActions", azDevIdentifier),
			MakeUserAgentString("template@0.0.1"),
		)
	})
}

func TestUserAgentString(t *testing.T) {
	userAgent := UserAgent{
		azDevCliIdentifier: "cli/1.0.0",
	}

	require.Equal(t, userAgent.String(), userAgent.azDevCliIdentifier)

	// Verify complete formatting
	userAgent = UserAgent{
		azDevCliIdentifier:      "cli/1.0.0",
		userSpecifiedIdentifier: "dev-user-agent/0.0.0-beta",
		templateIdentifier:      "template/mytemplate@2.0.0",
		githubActionsIdentifier: "gh/3.0.2",
	}

	require.Equal(
		t,
		userAgent.String(),
		fmt.Sprintf(
			"%s %s %s %s",
			userAgent.azDevCliIdentifier,
			userAgent.userSpecifiedIdentifier,
			userAgent.templateIdentifier,
			userAgent.githubActionsIdentifier,
		),
	)
}
