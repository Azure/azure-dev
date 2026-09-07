// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package servicekey contains helpers for azure.yaml service keys.
package servicekey

import "strings"

// SanitizeServiceName converts a resource name into an azure.yaml service key
// by trimming surrounding whitespace and removing interior spaces. Resource
// names are expected to otherwise consist of characters valid in a YAML map
// key (letters, digits, '-', '_', '.').
func SanitizeServiceName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), " ", "")
}
