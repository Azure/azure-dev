// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package envkey builds the TOOLBOX_<NAME>_MCP_ENDPOINT key.
//
// The key logic mirrors azure.ai.agents/internal/pkg/envkey: separate Go
// modules can't share it, yet both must agree on the key or the agent won't
// find the value the toolbox wrote. Keep in sync (the test mirrors the agents
// cases); intended to be lifted into a shared module later.
package envkey

import (
	"fmt"
	"regexp"
	"strings"
)

var nonAlphanumRe = regexp.MustCompile(`[^A-Z0-9]+`)

// ToolboxMCPEndpoint returns the canonical env-var key for a toolbox's MCP
// endpoint URL.
func ToolboxMCPEndpoint(toolboxName string) string {
	sanitized := nonAlphanumRe.ReplaceAllString(strings.ToUpper(toolboxName), "_")
	return fmt.Sprintf("TOOLBOX_%s_MCP_ENDPOINT", sanitized)
}
