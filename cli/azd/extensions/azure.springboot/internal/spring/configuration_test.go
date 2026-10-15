// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFlattenConfigurationLists(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values []any
		want   string
		reject bool
	}{
		{name: "strings", values: []any{"blue", "green"}, want: "blue,green"},
		{name: "numbers", values: []any{1, int64(2), 3.5}, want: "1,2,3.5"},
		{name: "booleans", values: []any{true, false}, want: "true,false"},
		{name: "mixed scalars", values: []any{"one", 2, true, 0.25}, want: "one,2,true,0.25"},
		{name: "empty", values: []any{}, want: ""},
		{name: "object element", values: []any{map[string]any{"key": "value"}}, reject: true},
		{name: "nested list", values: []any{[]any{1, 2}}, reject: true},
		{name: "null element", values: []any{nil}, reject: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := map[string]property{}
			err := flatten(map[string]any{"app": map[string]any{"values": tt.values}}, "", "application.yml", result)
			if tt.reject {
				require.ErrorContains(t, err, "non-scalar configuration list in application.yml")
				require.NotContains(t, result, "app.values")
				return
			}
			require.NoError(t, err)
			require.Equal(t, property{text: tt.want, source: "application.yml", key: "app.values"}, result["app.values"])
		})
	}
}
