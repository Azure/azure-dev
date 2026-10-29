// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"slices"
)

// SubstituteJSONStrings walks a decoded JSON value and applies fn to each string and object key. Numbers, booleans and
// null are returned as authored. The input is not modified. An error is returned when two distinct object keys resolve
// to the same string, because one value would otherwise silently replace the other.
func SubstituteJSONStrings(value any, fn func(string) (string, error)) (any, error) {
	switch v := value.(type) {
	case string:
		return fn(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			resolved, err := SubstituteJSONStrings(item, fn)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		sources := make(map[string]string, len(v))
		// Iterate in sorted order so collision errors are deterministic.
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			resolvedKey, err := fn(key)
			if err != nil {
				return nil, err
			}
			if previous, exists := sources[resolvedKey]; exists {
				return nil, fmt.Errorf(
					"object keys %q and %q both resolve to %q after substitution", previous, key, resolvedKey)
			}
			sources[resolvedKey] = key
			resolved, err := SubstituteJSONStrings(v[key], fn)
			if err != nil {
				return nil, err
			}
			out[resolvedKey] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}
