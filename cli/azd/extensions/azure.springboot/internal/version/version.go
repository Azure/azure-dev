// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package version

// Version, Commit, and BuildDate are populated at build time by release pipelines via ldflags.
var (
	Version   = "0.1.0"
	Commit    = "none"
	BuildDate = "unknown"
)
