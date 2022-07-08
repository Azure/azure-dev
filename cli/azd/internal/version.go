// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import "strings"

// Version is the version string printed out by the `Version` command.
// It's updated using ldflags in CI.
// Example:
//   -ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=0.0.1-alpha.1 (commit 8a49ae5ae9ab13beeade35f91ad4b4611c2f5574)'"
var Version = "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

// GetVersionNumber splits the cmd.Version string to get the
// semver for the command.
// Returns a version string like `0.0.1-alpha.1`.
func GetVersionNumber() string {
	pieces := strings.SplitN(Version, " ", 2)

	if len(pieces) < 2 {
		return "unknown"
	}

	return pieces[0]
}
