// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package events provides definitions and functions related to the definition of telemetry events.
package events

import "strings"

// Command event names follow the convention cmd.<command invocation path replaced by .>.
//
// Examples:
//   - cmd.infra.create
//   - cmd.init
//   - cmd.up
const CommandEventPrefix = "cmd."

// GetCommandEventName returns the event name for CLI commands.
func GetCommandEventName(cmdPath string) string {
	return CommandEventPrefix + getCommandPath(cmdPath)
}

// Removes `azd` from command path.
// Replaces spaces with dot.
// Example: `azd infra create` -> infra.create
func getCommandPath(cmdPath string) string {
	cmdPathElements := strings.Split(cmdPath, " ")
	// exclude first element, which is always `azd`
	return strings.Join(cmdPathElements[1:], ".")
}
