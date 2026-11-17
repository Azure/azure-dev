// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetScalarValue(t *testing.T) {
	t.Run("reads supported scalar values", func(t *testing.T) {
		// Read already-typed map values without converting between scalar types.
		value, err := getScalarValue[string](map[string]any{"provider": "resources"}, "provider")
		require.NoError(t, err)
		require.Equal(t, "resources", value)

		count, err := getScalarValue[int](map[string]any{"count": 3}, "count")
		require.NoError(t, err)
		require.Equal(t, 3, count)

		enabled, err := getScalarValue[bool](map[string]any{"enabled": true}, "enabled")
		require.NoError(t, err)
		require.True(t, enabled)
	})

	t.Run("reads string-ish type", func(t *testing.T) {
		// The ~string constraint also admits a named type when the stored value has that type.
		type provider string

		value, err := getScalarValue[provider](map[string]any{"provider": provider("resources")}, "provider")
		require.NoError(t, err)
		require.Equal(t, provider("resources"), value)
	})

	t.Run("missing value returns zero value", func(t *testing.T) {
		// An omitted optional field is not a type error.
		value, err := getScalarValue[string](map[string]any{}, "provider")
		require.NoError(t, err)
		require.Empty(t, value)
	})

	t.Run("bad value type returns error", func(t *testing.T) {
		// A present string must not be silently coerced to the requested integer type.
		_, err := getScalarValue[int](map[string]any{"count": "3"}, "count")
		require.EqualError(t, err, `"count" must be int`)
	})
}
