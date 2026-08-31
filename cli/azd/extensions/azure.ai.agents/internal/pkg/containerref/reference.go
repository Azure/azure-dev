// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package containerref validates container image references used by hosted agents.
package containerref

import (
	"strings"

	"github.com/distribution/reference"
)

// IsValid reports whether image is a syntactically valid named container image reference.
func IsValid(image string) bool {
	_, ok := parseNamed(image)
	return ok
}

// IsFullyQualified reports whether image is valid and contains an explicit registry host and repository.
func IsFullyQualified(image string) bool {
	named, ok := parseNamed(image)
	if !ok {
		return false
	}

	registry := reference.Domain(named)
	return registry != "" &&
		(strings.Contains(registry, ".") || strings.Contains(registry, ":") || registry == "localhost")
}

func parseNamed(image string) (reference.Named, bool) {
	parsed, err := reference.Parse(image)
	if err != nil {
		return nil, false
	}

	named, ok := parsed.(reference.Named)
	return named, ok
}
