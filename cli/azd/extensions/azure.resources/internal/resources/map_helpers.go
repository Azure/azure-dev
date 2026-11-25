// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"fmt"
)

type scalarValue interface {
	~string | ~bool |
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// getScalarValue returns the zero value when key is absent and an error when its value is not T.
func getScalarValue[T scalarValue](values map[string]any, key string) (T, error) {
	value, exists := values[key]
	if !exists {
		var zero T
		return zero, nil
	}

	typed, ok := value.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("%q must be %T", key, zero)
	}
	return typed, nil
}
