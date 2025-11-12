// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import "fmt"

// ExpandableMap is a map of string keys to ExpandableString values.
// It provides convenient methods for expanding all values in the map.
type ExpandableMap map[string]ExpandableString

// Expand evaluates all ExpandableString values in the map, substituting variables as [ExpandableString.Envsubst] would.
// Returns a map[string]string with all values expanded, or an error if any expansion fails.
// The mapping parameter is a function that returns the value for a given variable name.
func (em ExpandableMap) Expand(mapping func(string) string) (map[string]string, error) {
	result := make(map[string]string, len(em))
	for key, value := range em {
		expanded, err := value.Envsubst(mapping)
		if err != nil {
			return nil, fmt.Errorf("expanding %s: %w", key, err)
		}
		result[key] = expanded
	}
	return result, nil
}

// MustExpand evaluates all ExpandableString values in the map and panics if any expansion fails.
// This is useful when you know the expansion should succeed or want to fail fast.
func (em ExpandableMap) MustExpand(mapping func(string) string) map[string]string {
	result, err := em.Expand(mapping)
	if err != nil {
		panic(fmt.Sprintf("MustExpand: %v", err))
	}
	return result
}
