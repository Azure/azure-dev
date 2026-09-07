// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore envkey

// Package envkey builds the per-Connection readiness marker keys shared by
// the Connections and Agents extensions.
package envkey

import "fmt"

// ConnectionProjectEndpoint scopes a deployed Connection to its Foundry project.
// Encode the exact service-name bytes as uppercase hex so punctuation and case
// remain distinct even on Windows. Keep in sync with Agents' consumer helper.
// V2 markers intentionally do not reuse the old, ambiguous normalized keys.
func ConnectionProjectEndpoint(connectionName string) string {
	return fmt.Sprintf("CONNECTION_V2_%X_PROJECT_ENDPOINT", []byte(connectionName))
}
