// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package events

import "strings"

// GetCommandEventName returns the event name for CLI commands.
func GetCommandEventName(cmdPath string) string {
	return CommandEventPrefix + formatCommandPath(cmdPath)
}

// formatCommandPath reformats the command path suitable for telemetry emission.
//
// It removes "azd" from command path and replaces spaces with dot.
//
// Example: "azd env list" -> "env.list"
func formatCommandPath(cmdPath string) string {
	cmdPath = strings.TrimPrefix(cmdPath, "azd ")
	return strings.ReplaceAll(cmdPath, " ", ".")
}
