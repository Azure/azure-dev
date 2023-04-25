// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestActionResult_ToString(t *testing.T) {
	tests := []struct {
		name     string
		ar       *ActionResult
		expected string
	}{
		{
			name: "Success",
			ar: &ActionResult{
				SuccessMessage: "A success!",
			},
			expected: output.WithSuccessFormat("\n%s: %s", "SUCCESS", "A success!"),
		},
		{
			name: "SuccessWithFollowUp",
			ar: &ActionResult{
				SuccessMessage: "A success!",
				FollowUp:       "A follow up!",
			},
			expected: output.WithSuccessFormat("\n%s: %s", "SUCCESS", "A success!") + "\nA follow up!",
		},
		{
			name: "Error",
			ar: &ActionResult{
				Err: errors.New("An error :("),
			},
			expected: output.WithErrorFormat("\n%s: %s", "ERROR", "An error :("),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.ar.ToString(""))
		})
	}
}
