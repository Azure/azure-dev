// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Validation error type
// ---------------------------------------------------------------------------

// ValidationError describes a failed input validation with structured context.
type ValidationError struct {
	// Field is the logical name of the input being validated (e.g. "service_name").
	Field string

	// Value is the rejected input value. For security-sensitive inputs the value
	// may be truncated or redacted by the caller before constructing the error.
	Value string

	// Rule is a short machine-readable tag for the violated constraint
	// (e.g. "format", "length", "characters").
	Rule string

	// Message is a human-readable explanation suitable for end-user display.
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("azdext.Validate: %s %q: %s (%s)", e.Field, e.Value, e.Message, e.Rule)
}

// ---------------------------------------------------------------------------
// Service name validation
// ---------------------------------------------------------------------------

// serviceNameRe matches DNS-safe service names:
//   - starts with alphanumeric
//   - contains only alphanumeric, '.', '_', '-'
//   - 1–63 characters total (DNS label limit)
var serviceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// ValidateServiceName checks that name is a valid DNS-safe service identifier.
//
// Rules:
//   - Must start with an alphanumeric character.
//   - May contain alphanumeric characters, '.', '_', and '-'.
//   - Must be 1–63 characters (DNS label length limit per RFC 1035).
//
// Returns a [*ValidationError] on failure.
func ValidateServiceName(name string) error {
	if name == "" {
		return &ValidationError{
			Field:   "service_name",
			Value:   "",
			Rule:    "required",
			Message: "service name must not be empty",
		}
	}

	if !serviceNameRe.MatchString(name) {
		return &ValidationError{
			Field:   "service_name",
			Value:   truncateValue(name, 64),
			Rule:    "format",
			Message: "service name must start with alphanumeric and contain only [a-zA-Z0-9._-], max 63 chars",
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Hostname validation
// ---------------------------------------------------------------------------

// hostnameRe matches RFC 952/1123 hostnames:
//   - labels separated by '.'
//   - each label: starts and ends with alphanumeric, may contain '-', 1–63 chars
//   - total length <= 253 characters
var hostnameRe = regexp.MustCompile(
	`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?` +
		`(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`,
)

// ValidateHostname checks that hostname conforms to RFC 952/1123.
//
// Rules:
//   - Each label must start and end with an alphanumeric character.
//   - Labels may contain alphanumeric characters and '-'.
//   - Each label is 1–63 characters.
//   - Total hostname length is ≤ 253 characters.
//
// Returns a [*ValidationError] on failure.
func ValidateHostname(hostname string) error {
	if hostname == "" {
		return &ValidationError{
			Field:   "hostname",
			Value:   "",
			Rule:    "required",
			Message: "hostname must not be empty",
		}
	}

	if len(hostname) > 253 {
		return &ValidationError{
			Field:   "hostname",
			Value:   truncateValue(hostname, 64),
			Rule:    "length",
			Message: "hostname must not exceed 253 characters",
		}
	}

	if !hostnameRe.MatchString(hostname) {
		return &ValidationError{
			Field: "hostname",
			Value: truncateValue(hostname, 64),
			Rule:  "format",
			Message: "hostname must conform to RFC 952/1123 " +
				"(labels: alphanumeric start/end, may contain '-', 1-63 chars each)",
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Script name validation
// ---------------------------------------------------------------------------

// shellMetacharacters contains characters that have special meaning in common
// shells (bash, sh, zsh, cmd, PowerShell). A script name containing any of
// these is rejected to prevent command injection.
const shellMetacharacters = ";|&`$(){}[]<>!#~*?\"\\'%\n\r\x00"

// ValidateScriptName checks that name does not contain shell metacharacters
// or path traversal sequences that could lead to command injection.
//
// Rejected patterns:
//   - Shell metacharacters: ; | & ` $ ( ) { } [ ] < > ! # ~ * ? " ' \ %
//   - Path traversal: ".."
//   - Null bytes and newlines
//   - Empty names
//
// Returns a [*ValidationError] on failure.
func ValidateScriptName(name string) error {
	if name == "" {
		return &ValidationError{
			Field:   "script_name",
			Value:   "",
			Rule:    "required",
			Message: "script name must not be empty",
		}
	}

	if strings.Contains(name, "..") {
		return &ValidationError{
			Field:   "script_name",
			Value:   truncateValue(name, 64),
			Rule:    "traversal",
			Message: "script name must not contain path traversal sequences (..)",
		}
	}

	if idx := strings.IndexAny(name, shellMetacharacters); idx >= 0 {
		return &ValidationError{
			Field:   "script_name",
			Value:   truncateValue(name, 64),
			Rule:    "characters",
			Message: fmt.Sprintf("script name contains forbidden shell metacharacter at position %d", idx),
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Container environment detection
// ---------------------------------------------------------------------------

// containerEnvVars maps environment variables to the container runtime they indicate.
var containerEnvVars = map[string]string{
	"CODESPACES":              "codespaces",
	"KUBERNETES_SERVICE_HOST": "kubernetes",
	"REMOTE_CONTAINERS":       "devcontainer",
	"REMOTE_CONTAINERS_IPC":   "devcontainer",
}

// IsContainerEnvironment reports whether the current process is running inside
// a container environment. It checks for:
//   - GitHub Codespaces (CODESPACES env var)
//   - Kubernetes (KUBERNETES_SERVICE_HOST env var)
//   - VS Code Dev Containers (REMOTE_CONTAINERS / REMOTE_CONTAINERS_IPC env vars)
//   - Docker (/.dockerenv file)
//
// The detection is best-effort and does not guarantee accuracy in all
// environments. It is intended for feature gating and diagnostics, not
// security decisions.
func IsContainerEnvironment() bool {
	// Check well-known environment variables.
	for envKey := range containerEnvVars {
		if v := os.Getenv(envKey); v != "" {
			return true
		}
	}

	// Check for Docker's marker file.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	return false
}

// ContainerRuntime returns the detected container runtime name, or an empty
// string if no container environment is detected.
//
// Possible return values: "codespaces", "kubernetes", "devcontainer", "docker", "".
func ContainerRuntime() string {
	for envKey, runtime := range containerEnvVars {
		if v := os.Getenv(envKey); v != "" {
			return runtime
		}
	}

	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}

	return ""
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// truncateValue truncates s to maxLen characters for safe inclusion in error
// messages. If truncated, an ellipsis is appended.
func truncateValue(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
