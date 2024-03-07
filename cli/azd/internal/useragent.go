package internal

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Environment variable that identifies a user agent calling into azd.
// Any caller of azd can set this variable to identify themselves.
const AzdUserAgentEnvVar = "AZURE_DEV_USER_AGENT"

// Well-known user agents prefixes.
const (
	VsCodeAgentPrefix = "vscode:/extensions/ms-azuretools.azure-dev"
	// cspell: disable-next-line
	VsAgentPrefix = "vside:/webtools/azdev.publish"
)

// UserAgent() creates the user agent string for azd.
//
// Examples:
//   - azdev/1.0.0 (Go 1.18; windows/amd64)
//   - azdev/1.0.0 (Go 1.18; windows/amd64) azd-caller/1.0.0
func UserAgent() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("azdev/%s", VersionInfo().Version.String()))
	sb.WriteString(" ")
	sb.WriteString(runtimeInfo())
	callerAgent := os.Getenv(AzdUserAgentEnvVar)
	if callerAgent != "" {
		sb.WriteString(" ")
		sb.WriteString(callerAgent)
	}

	if strings.ToLower(os.Getenv("GITHUB_ACTIONS")) == "true" {
		sb.WriteString(" ")
		sb.WriteString("GhActions")
	}

	return sb.String()
}

func runtimeInfo() string {
	return fmt.Sprintf("(Go %s; %s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
