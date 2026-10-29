// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubstituteJSONStrings(t *testing.T) {
	upper := func(s string) (string, error) { return strings.ToUpper(s), nil }

	got, err := SubstituteJSONStrings(map[string]any{
		"a": "x",
		"b": []any{"y", float64(1), true, nil},
		"c": map[string]any{"k": "z"},
	}, upper)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"A": "X",
		"B": []any{"Y", float64(1), true, nil},
		"C": map[string]any{"K": "Z"},
	}, got)
}

func TestSubstituteJSONStrings_KeyCollisionIsAnError(t *testing.T) {
	lower := func(s string) (string, error) { return strings.ToLower(s), nil }

	_, err := SubstituteJSONStrings(map[string]any{"Key": "1", "key": "2"}, lower)
	require.ErrorContains(t, err, `"Key" and "key" both resolve to "key"`)
}
