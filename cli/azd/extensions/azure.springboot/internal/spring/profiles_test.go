// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpringProfileExpressions(t *testing.T) {
	for _, tt := range []struct {
		expression string
		match      bool
		invalid    bool
	}{
		{"release", true, false},
		{"local", false, false},
		{"release & !local", true, false},
		{"(release & !local) | fallback", true, false},
		{"release | (local & fallback)", true, false},
		{"local,release", true, false},
		{"release & local | fallback", false, true},
		{"release | local & fallback", false, true},
		{"release &", false, true},
		{"(release", false, true},
		{"", false, true},
	} {
		t.Run(tt.expression, func(t *testing.T) {
			match, err := matchesProfile(tt.expression, map[string]bool{"release": true})
			if tt.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.match, match)
			}
		})
	}
}
