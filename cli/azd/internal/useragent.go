package internal

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const userSpecifiedAgentEnvironmentVariableName = "AZURE_DEV_USER_AGENT"
const githubActionsEnvironmentVariableName = "GITHUB_ACTIONS"

const azDevProductIdentifierKey = "azdev"
const templateProductIdentifierKey = "azdtempl"
const githubActionsProductIdentifierKey = "GhActions"

type UserAgent struct {
	// Azure Developer CLI product identifier. Formatted as `azdev/<version>`
	azDevCliIdentifier string

	// (Optional) User specified identifier, set from `AZURE_DEV_USER_AGENT` environment variable
	userSpecifiedIdentifier string

	// (Optional) Identifier for the template used, if applicable. Formatted as `azdtempl/<version>`
	templateIdentifier string

	// (Optional) Identifier for GitHub Actions, if applicable
	githubActionsIdentifier string
}

func (userAgent *UserAgent) String() string {
	var sb strings.Builder
	sb.WriteString(userAgent.azDevCliIdentifier)
	appendIdentifier(&sb, userAgent.userSpecifiedIdentifier)
	appendIdentifier(&sb, userAgent.templateIdentifier)
	appendIdentifier(&sb, userAgent.githubActionsIdentifier)

	return sb.String()
}

const (
	templateContextKey contextKey = "template"
)

func WithTemplate(ctx context.Context, template string) context.Context {
	return context.WithValue(ctx, templateContextKey, template)
}

func GetTemplate(ctx context.Context) string {
	template, ok := ctx.Value(templateContextKey).(string)
	if !ok {
		return ""
	}

	return template
}

func appendIdentifier(sb *strings.Builder, identifier string) {
	if identifier != "" {
		sb.WriteString(" " + identifier)
	}
}

func makeUserAgent(template string) UserAgent {
	userAgent := UserAgent{}
	userAgent.azDevCliIdentifier = getAzDevCliIdentifier()
	userAgent.userSpecifiedIdentifier = getUserSpecifiedIdentifier()
	userAgent.githubActionsIdentifier = getGithubActionsIdentifier()
	userAgent.templateIdentifier = formatTemplateIdentifier(template)

	return userAgent
}

// MakeUserAgentString creates a user agent string that contains all necessary product identifiers, in increasing order:
// - The Azure Developer CLI version, formatted as `azdev/<version>`
// - The user specified identifier, set from `AZURE_DEV_USER_AGENT` environment variable
// - The identifier for the template used, if applicable
// - The identifier for GitHub Actions, if applicable
// Examples (see test `TestUserAgentStringScenarios` for all scenarios ):
// - `azdev/1.0.0 (Go 1.18; windows/amd64)`
// - `azdev/1.0.0 (Go 1.18; windows/amd64) Custom-foo/1.0.0 azdtempl/my-template@1.0.0 GhActions`
func MakeUserAgentString(template string) string {
	userAgent := makeUserAgent(template)

	return userAgent.String()
}

func getAzDevCliIdentifier() string {
	return fmt.Sprintf("%s/%s %s", azDevProductIdentifierKey, GetVersionNumber(), getPlatformInfo())
}

func getPlatformInfo() string {
	return fmt.Sprintf("(Go %s; %s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func getUserSpecifiedIdentifier() string {
	// like the Azure CLI (via it's `AZURE_HTTP_USER_AGENT` env variable) we allow for a user to append
	// information to the UserAgent by setting an environment variable.
	if devUserAgent := os.Getenv(userSpecifiedAgentEnvironmentVariableName); devUserAgent != "" {
		return devUserAgent
	}

	return ""
}

func getGithubActionsIdentifier() string {
	// `GITHUB_ACTIONS` must be set to 'true' if running in GitHub Actions,
	// see https://docs.github.com/en/actions/learn-github-actions/environment-variables#default-environment-variables
	if isRunningInGithubActions := os.Getenv(githubActionsEnvironmentVariableName); isRunningInGithubActions == "true" {
		return githubActionsProductIdentifierKey
	}

	return ""
}

func formatTemplateIdentifier(template string) string {
	if template == "" {
		return ""
	}

	return fmt.Sprintf("%s/%s", templateProductIdentifierKey, template)
}
