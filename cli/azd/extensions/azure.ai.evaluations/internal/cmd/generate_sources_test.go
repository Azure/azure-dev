// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's default: traces when the project has Application Insights
// connected, otherwise the agent. The connection string is how a project says
// it collects traces at all, so asking for traces without one would submit a
// billed job against nothing.
func TestDefaultGenerationSource(t *testing.T) {
	assert.Equal(t, []string{"traces"},
		defaultGenerationSource("InstrumentationKey=00000000-0000-0000-0000-000000000000"),
		"a project collecting traces should be generated from them")

	assert.Equal(t, []string{"agent"}, defaultGenerationSource(""),
		"with nowhere for traces to have been collected, the agent is all there is")
}

// --from is a request, and one the plan cannot honour has to stop the command
// rather than quietly submit a job seeded from less than was asked for.
func TestRefuseUnbuildableSources(t *testing.T) {
	assert.NoError(t, refuseUnbuildableSources(nil))
	assert.NoError(t, refuseUnbuildableSources([]string{}))

	tests := []struct {
		kind string
		says string
	}{
		{"prompt", "--agent-instruction"},
		{"agent", "--target"},
		{"file", "azd ai eval dataset create"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			err := refuseUnbuildableSources([]string{tt.kind})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.says,
				"the error has to name the way out, not just the problem")
		})
	}
}

// Two unhonoured sources are two things the caller has to fix, so both are
// reported at once rather than one per attempt.
func TestRefuseUnbuildableSources_ReportsAllOfThemAtOnce(t *testing.T) {
	err := refuseUnbuildableSources([]string{"prompt", "agent"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--agent-instruction")
	assert.Contains(t, err.Error(), "--target")
}
