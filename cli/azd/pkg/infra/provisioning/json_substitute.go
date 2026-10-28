// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

// SubstituteJSONStrings walks a decoded JSON value and applies fn to each string and object key. Numbers, booleans and
// null are returned as authored. The input is not modified.
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
