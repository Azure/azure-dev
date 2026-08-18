// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package containerref validates container image references used by hosted agents.
package containerref

// cSpell:ignore containerref

import "regexp"

var fullyQualifiedReference = regexp.MustCompile(
	`^(?:(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?::[0-9]+)?|` +
		`localhost(?::[0-9]+)?|[a-z0-9](?:[a-z0-9-]*[a-z0-9])?:[0-9]+)/` +
		`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*` +
		`(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
		`(?::[\w][\w.-]{0,127}|@sha256:[0-9a-fA-F]{64})?$`,
)

// IsFullyQualified reports whether image contains an explicit registry host and repository.
func IsFullyQualified(image string) bool {
	return fullyQualifiedReference.MatchString(image)
}
