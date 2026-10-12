// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

func TestHasUsageMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		usage    *v1beta.CopilotUsageMetrics
		expected bool
	}{
		{name: "Nil", expected: false},
		{name: "Empty", usage: &v1beta.CopilotUsageMetrics{}, expected: false},
		{name: "InputTokens", usage: &v1beta.CopilotUsageMetrics{InputTokens: 1}, expected: true},
		{name: "OutputTokens", usage: &v1beta.CopilotUsageMetrics{OutputTokens: 1}, expected: true},
		{name: "AICredits", usage: &v1beta.CopilotUsageMetrics{AiCredits: 0.25}, expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.expected, hasUsageMetrics(test.usage))
		})
	}
}
