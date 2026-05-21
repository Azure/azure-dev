// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package version exposes the extension version for User-Agent strings and
// telemetry. Populated at build time via -ldflags.
package version

var (
	// Version is the extension semver, populated at build time.
	Version = "dev"
	// Commit is the source commit hash, populated at build time.
	Commit = "none"
	// BuildDate is an ISO-8601 build timestamp, populated at build time.
	BuildDate = "unknown"
)
