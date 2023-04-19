// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"regexp"
	"strings"

	"github.com/blang/semver/v4"
)

// cDevVersionString is the default version that is used when [Version] is not overridden at build time, i.e.
// a developer building locally using `go install`.
const cDevVersionString = "0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

// The version string, as printed by `azd version`.
//
// This MUST be of the form "<semver> (commit <full commit hash>)"
//
// The default value here is used for a version built directly by a developer when running either
// `go install` or `go build` without overriding the value at link time (the default behavior when
// build or install are run without arguments).
//
// Official builds set this value based on the version and commit we are building, using `-ldflags`
//
// Example:
//
//	-ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=0.0.1-alpha.1 (commit 8a49ae5ae9ab13beeade35f91ad4b4611c2f5574)'"
//
// This value is exported and not const so it can be mutated by certain tests. Instead of accessing this member
// directly, use [VersionInfo] which returns a structured version of this value.
//
// nolint: lll
var Version = cDevVersionString

func init() {
	// VersionInfo panics if the version string is malformed, run the code at package startup to
	// ensure everything is okay. This allows the rest of the system to call VersionInfo() to get
	// parsed version information without having to worry about error handling.
	_ = VersionInfo()
}

type AzdVersionInfo struct {
	Version semver.Version
	Commit  string
}

func IsDevVersion() bool {
	return Version == cDevVersionString
}

func IsNonProdVersion() bool {
	if IsDevVersion() {
		return true
	}

	// This currently relies on checking for specific internal release tags.
	// This can be improved to instead check for any presence of prerelease versioning
	// once the product is GA.
	return strings.Contains(VersionInfo().Version.String(), "pr")
}

var cVersionStringRegexp = regexp.MustCompile(`^(\S+) \(commit ([0-9a-f]{40})\)$`)

func VersionInfo() AzdVersionInfo {
	matches := cVersionStringRegexp.FindStringSubmatch(Version)

	if len(matches) != 3 {
		panic("azd version is malformed, ensure github.com/azure/azure-dev/cli/azd/internal.Version is correct")
	}

	return AzdVersionInfo{
		Version: semver.MustParse(matches[1]),
		Commit:  matches[2],
	}
}
