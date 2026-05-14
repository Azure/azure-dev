// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package envkey produces the canonical environment-variable keys that
// azd's hosted-agent toolbox flow reads and writes.
//
// Both the provisioning side (init.go injecting `TOOLBOX_<NAME>_MCP_ENDPOINT`
// references into the agent manifest, listen.go writing each value at
// runtime) and the diagnostic side (doctor `local.toolboxes` checking
// that those vars are set) must agree byte-for-byte on the key name —
// any divergence produces a false-negative diagnostic where the value
// is present under a different key than the check looks for. This
// package is the single source of truth for that key.
package envkey

import (
	"fmt"
	"regexp"
	"strings"
)

// nonAlphanumRe matches one or more characters that are not an
// upper-case ASCII letter or digit. Runs of such characters collapse
// to a single underscore — e.g. "my--tool" -> "MY_TOOL", "my(tool)" ->
// "MY_TOOL_", "my+tool" -> "MY_TOOL".
var nonAlphanumRe = regexp.MustCompile(`[^A-Z0-9]+`)

// ToolboxMCPEndpoint returns the canonical env-var key for a hosted
// toolbox's MCP endpoint URL. The convention is:
//
//	TOOLBOX_<sanitize(upper(name))>_MCP_ENDPOINT
//
// where sanitize collapses any run of non-`[A-Z0-9]` characters to a
// single underscore. Examples:
//
//	"web-search-tools" -> "TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"
//	"my tools"         -> "TOOLBOX_MY_TOOLS_MCP_ENDPOINT"
//	"my--tool"         -> "TOOLBOX_MY_TOOL_MCP_ENDPOINT"
//	"my:tool"          -> "TOOLBOX_MY_TOOL_MCP_ENDPOINT"
func ToolboxMCPEndpoint(toolboxName string) string {
	sanitized := nonAlphanumRe.ReplaceAllString(strings.ToUpper(toolboxName), "_")
	return fmt.Sprintf("TOOLBOX_%s_MCP_ENDPOINT", sanitized)
}
