// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// LoadAzdEnvironment loads environment variables from the current azd environment.
// It runs "azd env get-values" and parses the KEY=VALUE output.
// Returns a map of environment variable names to values.
func LoadAzdEnvironment(ctx context.Context) (map[string]string, error) {
	//nolint:gosec // G204: "azd" is a known, fixed program name — not user input.
	cmd := exec.CommandContext(ctx, "azd", "env", "get-values")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("azdext.LoadAzdEnvironment: failed to run 'azd env get-values': %w", err)
	}

	lines := strings.Split(string(output), "\n")

	return ParseEnvironmentVariables(lines), nil
}

// ParseEnvironmentVariables parses a slice of KEY=VALUE strings into a map.
// Values may optionally be quoted with double quotes, which are stripped.
// Empty lines and comment lines (starting with #) are skipped.
func ParseEnvironmentVariables(envVars []string) map[string]string {
	result := make(map[string]string, len(envVars))

	for _, raw := range envVars {
		line := strings.TrimSpace(raw)

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Strip surrounding double quotes from the value.
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		if key != "" {
			result[key] = value
		}
	}

	return result
}
