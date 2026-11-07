// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import "errors"

// SubstituteJSONStrings walks a decoded JSON value and applies fn to each string and object key. Numbers, booleans and
// null are returned as authored. The input is not modified. An error is returned when two distinct object keys resolve
// to the same string, because one value would otherwise silently replace the other. The error names no keys, since
// resolved keys can hold secrets.
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
		for key, item := range v {
			resolvedKey, err := fn(key)
			if err != nil {
				return nil, err
			}
			if _, exists := out[resolvedKey]; exists {
				return nil, errors.New("two object keys resolve to the same key after environment variable substitution")
			}
			resolved, err := SubstituteJSONStrings(item, fn)
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
