// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore envkey Alphanum

// Package envkey builds the per-Connection readiness marker keys shared by
// the Connections and Agents extensions.
package envkey

import (
	"fmt"
	"regexp"
	"strings"
)

var nonAlphanumRe = regexp.MustCompile(`[^A-Z0-9]+`)

// ConnectionProjectEndpoint scopes a deployed Connection to its Foundry project.
func ConnectionProjectEndpoint(connectionName string) string {
	sanitized := nonAlphanumRe.ReplaceAllString(strings.ToUpper(connectionName), "_")
	return fmt.Sprintf("CONNECTION_%s_PROJECT_ENDPOINT", sanitized)
}
