package internal

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAzDevCliIdentifier(t *testing.T) {
	version := GetVersionNumber()
	require.NotEmpty(t, version)

	require.Equal(t, fmt.Sprintf("%s/%s %s", azDevProductIdentifierKey, version, getPlatformInfo()), getAzDevCliIdentifier())
}

func TestUserSpecifiedAgentIdentifier(t *testing.T) {
	devUserAgent := "MyAgent/1.0.0"
	restorer := EnvironmentVariablesSetter(map[string]string{
		userSpecifiedAgentEnvironmentVariableName: devUserAgent,
	})

	require.Equal(t, devUserAgent, getUserSpecifiedIdentifier())

	// Empty case
	os.Setenv(userSpecifiedAgentEnvironmentVariableName, "")
	require.Equal(t, "", getUserSpecifiedIdentifier())

	t.Cleanup(restorer)
}

func TestGithubActionIdentifier(t *testing.T) {
	restorer := EnvironmentVariablesSetter(map[string]string{
		githubActionsEnvironmentVariableName: "",
	})

	require.Equal(t, "", getGithubActionsIdentifier())

	// Empty case
	os.Setenv(githubActionsEnvironmentVariableName, "true")
	require.Equal(t, "GhActions", getGithubActionsIdentifier())

	t.Cleanup(restorer)
}

func TestFormatTemplate(t *testing.T) {
	require.Equal(t, "", formatTemplateIdentifier(""))
	require.Equal(t, fmt.Sprintf("%s/todo-python-mongo", templateProductIdentifierKey), formatTemplateIdentifier("todo-python-mongo"))
	require.Equal(t, fmt.Sprintf("%s/todo-csharp-sql@0.0.1-beta", templateProductIdentifierKey), formatTemplateIdentifier("todo-csharp-sql@0.0.1-beta"))
}

// Scenario tests
func TestUserAgentStringScenarios(t *testing.T) {
	restorer := EnvironmentVariablesSetter(map[string]string{
		userSpecifiedAgentEnvironmentVariableName: "",
		githubActionsEnvironmentVariableName:      "",
	})

	version := GetVersionNumber()
	require.NotEmpty(t, version)

	azDevIdentifier := fmt.Sprintf("azdev/%s %s", version, getPlatformInfo())

	// Scenario: default agent
	require.Equal(t, azDevIdentifier, MakeUserAgentString(""))

	// Scenario: user specifies agent variable
	os.Setenv(userSpecifiedAgentEnvironmentVariableName, "dev_user_agent")
	require.Equal(t, fmt.Sprintf("%s dev_user_agent", azDevIdentifier), MakeUserAgentString(""))
	os.Setenv(userSpecifiedAgentEnvironmentVariableName, "")

	// Scenario: running on github actions
	os.Setenv(githubActionsEnvironmentVariableName, "true")
	require.Equal(t, fmt.Sprintf("%s GhActions", azDevIdentifier), MakeUserAgentString(""))
	os.Setenv(githubActionsEnvironmentVariableName, "")

	// Scenario: template present
	require.Equal(t, fmt.Sprintf("%s azdtempl/template@0.0.1", azDevIdentifier), MakeUserAgentString("template@0.0.1"))

	// Scenario: full combination
	os.Setenv(userSpecifiedAgentEnvironmentVariableName, "dev_user_agent")
	os.Setenv(githubActionsEnvironmentVariableName, "true")
	require.Equal(t, fmt.Sprintf("%s dev_user_agent azdtempl/template@0.0.1 GhActions", azDevIdentifier), MakeUserAgentString("template@0.0.1"))

	t.Cleanup(restorer)
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
		fmt.Sprintf("%s %s %s %s", userAgent.azDevCliIdentifier, userAgent.userSpecifiedIdentifier, userAgent.templateIdentifier, userAgent.githubActionsIdentifier))
}

// EnvironmentVariablesSetter sets the provided environment variables,
// returning a function that restores the environment variables to their original values.
// Example usage:
//
//	fn test(t *testing.T) {
//	  closer := helpers.EnvironmentVariablesSetter(map[string]string { "FOO_ENV": "Bar", "OTHER_FOO_ENV": "Bar2"})
//	  require.Equal(t, os.GetEnv("FOO_ENV"), "Bar")
//	  require.Equal(t, os.GetEnv("OTHER_FOO_ENV"), "Bar2")
//	  t.Cleanup(closer)
//	}
func EnvironmentVariablesSetter(envContext map[string]string) func() {
	restoreContext := map[string]string{}
	for key, value := range envContext {
		orig, present := os.LookupEnv(key)
		if present {
			restoreContext[key] = orig
		}

		os.Setenv(key, value)
	}

	return func() {
		for key := range envContext {
			if restoreValue, present := restoreContext[key]; present {
				os.Setenv(key, restoreValue)
			} else {
				os.Unsetenv(key)
			}
		}
	}
}
