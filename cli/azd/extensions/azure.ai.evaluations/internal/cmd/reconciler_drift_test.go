// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Drift is only interesting when the two definitions already disagree, and
// then only when the disagreement came from the project rather than from the
// author. These are the four ways that question can be answered.
func TestCheckEvaluatorDrift(t *testing.T) {
	// The author edited the file. The project is where the last deploy left
	// it, so publishing is exactly right and must not be blocked.
	require.NoError(t, checkEvaluatorDrift("support-quality", "3", "3"))

	// Someone published outside the repo. Publishing over it would leave
	// their change behind with `azd up` reporting success.
	err := checkEvaluatorDrift("support-quality", "3", "4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "support-quality")
	assert.Contains(t, err.Error(), "version 4")
	assert.Contains(t, err.Error(), "3 was recorded")
	assert.Contains(t, err.Error(), "outside this configuration",
		"the message has to say what moved, not just that something did")

	// A version that went backwards is not drift: a newer version was
	// deleted, and republishing is how the repo takes the name back.
	require.NoError(t, checkEvaluatorDrift("support-quality", "4", "3"))

	// Versions this extension did not number cannot be compared, and refusing
	// a deploy over a numbering convention it does not own would be worse
	// than not checking.
	require.NoError(t, checkEvaluatorDrift("support-quality", "", "4"))
	require.NoError(t, checkEvaluatorDrift("support-quality", "3", "preview"))
}
