// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"regexp"
	"strings"
)

// Version is the version string printed out by the `Version` command.
// It's updated using ldflags in CI.
// Example:
//
//	-ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=0.0.1-alpha.1 (commit 8a49ae5ae9ab13beeade35f91ad4b4611c2f5574)'"
var Version = "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

const UnknownVersion string = "unknown"
const UnknownCommit string = "unknown"

type VersionSpec struct {
	Azd AzdVersionSpec `json:"azd"`
}

type AzdVersionSpec struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// GetVersionNumber splits the cmd.Version string to get the
// semver for the command.
// Returns a version string like `0.0.1-alpha.1`.
func GetVersionNumber() string {
	pieces := strings.SplitN(Version, " ", 2)

	if len(pieces) < 2 {
		return UnknownVersion
	}

	return pieces[0]
}

// Non-whitespace (version number), followed by some whitespace, followed by open parenthesis, optional whitespace, and word 'commit',
// followed by not-whitespace, not-closing-parenthesis (commit hash), followed by optional whitespace and closing parenthesis.
var azdVersionStrRegex = regexp.MustCompile(`(\S+)\s+\(\s*commit\s+([^)\s]+)\s*\)`)

func GetVersionSpec() VersionSpec {
	matches := azdVersionStrRegex.FindStringSubmatch(Version)
	if matches == nil {
		return VersionSpec{
			Azd: AzdVersionSpec{
				Version: UnknownVersion,
				Commit:  UnknownCommit,
			},
		}
	} else {
		return VersionSpec{
			Azd: AzdVersionSpec{
				Version: matches[1],
				Commit:  matches[2],
			},
		}
	}
}
