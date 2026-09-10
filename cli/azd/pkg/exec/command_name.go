// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"path/filepath"
	"strings"
)

// CommandName returns a safe, normalized command name for telemetry.
// It removes the path and executable extension and lowercases the result.
func CommandName(command string) string {
	command = filepath.Base(strings.ReplaceAll(command, "\\", "/"))
	if len(command) > 0 && command[0] == '.' {
		if len(command) == 1 {
			return ""
		}

		command = command[1:]
	}

	for i := range command {
		if command[i] == '.' {
			command = command[:i]
			break
		}
	}

	return strings.ToLower(command)
}
