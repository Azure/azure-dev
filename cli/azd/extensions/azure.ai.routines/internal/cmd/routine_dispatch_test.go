// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDispatchInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"plain-string", "hello world", "hello world"},
		{"empty", "", ""},
		{"json-object", `{"k":"v"}`, map[string]any{"k": "v"}},
		{"json-array", `[1,2,3]`, []any{1.0, 2.0, 3.0}},
		{"json-string", `"quoted"`, "quoted"},
		{"json-number", "42", 42.0},
		{"json-true", "true", true},
		{"json-false", "false", false},
		{"json-null", "null", nil},
		{"json-invalid-fallback", "{not-json", "{not-json"},
		{"identifier-starting-with-t", "tomorrow", "tomorrow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseDispatchInput(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
