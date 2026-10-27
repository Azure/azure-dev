// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceLocations(t *testing.T) {
	const body = "{\r\n  \"private.é\": {\"type\": \"currency\"},\r\n" +
		"  \"private\": {\"é\": {\"type\": \"extarct\"}},\r\n" +
		"  \"array\": [{\"type\": \"bad\"}],\r\n" +
		"  \"escaped\\\"name\": {\"type\": 123456789012345678901234567890}\r\n}"
	locations, err := SourceLocations([]byte(body))
	require.NoError(t, err)
	for _, test := range []struct {
		path, want string
	}{
		{"$", "Line 1, column 1"},
		{"private.é.type", "Line 2, column 17; Line 3, column 21"},
		{"private.é.items", "Line 2, column 3; Line 3, column 15"},
		{"array.0.type", "Line 4, column 14"},
		{`escaped"name.type`, "Line 5, column 21"},
		{"baseAnalyzerId", "baseAnalyzerId"},
	} {
		t.Run(test.path, func(t *testing.T) {
			label := (Finding{Path: test.path}).DiagnosticLocation(locations)
			require.Equal(t, test.want, label)
			require.NotContains(t, label, "private")
			require.NotContains(t, label, "currency")
			require.NotContains(t, label, "extarct")
		})
	}
}

func TestSourceLocationsInvalidJSON(t *testing.T) {
	_, err := SourceLocations([]byte(`{"private":`))
	require.Error(t, err)
}
