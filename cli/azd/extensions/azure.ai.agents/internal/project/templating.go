// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "github.com/azure/azure-dev/cli/azd/pkg/foundry"

// ExpandEnv re-exports the shared Foundry expander so every Foundry field in this
// extension expands ${VAR} (against the azd environment) while preserving Foundry
// server-side ${{...}} expressions verbatim. The implementation is shared across
// the Foundry extensions in [foundry.ExpandEnv].
func ExpandEnv(value string, mapping func(string) string) (string, error) {
	return foundry.ExpandEnv(value, mapping)
}
