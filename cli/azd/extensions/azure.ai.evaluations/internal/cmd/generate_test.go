// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// The service's system error says only that something went wrong and to try
// again, but agent-seeded generation fails deterministically, so a bare retry
// suggestion sends users into a loop.
func TestExplainDataGenerationFailureAddsAgentContext(t *testing.T) {
	err := errors.New(
		`job failed with status "failed": Something went wrong during data generation. Please try again.`)

	explained := explainDataGenerationFailure(err, "my-agent")
	require.Error(t, explained)
	require.Contains(t, explained.Error(), "my-agent")
	require.Contains(t, explained.Error(), "--dataset")
	require.ErrorIs(t, explained, err, "the original error must stay in the chain")
}

// The code spelling is matched as well, in case the poller starts surfacing it.
func TestExplainDataGenerationFailureMatchesErrorCode(t *testing.T) {
	err := fmt.Errorf("job failed: DataGenerationJobSystemError")
	explained := explainDataGenerationFailure(err, "my-agent")
	require.Contains(t, explained.Error(), "Workarounds")
}

// Unrelated failures are passed through untouched, and so is a job that had no
// agent source to blame.
func TestExplainDataGenerationFailureLeavesOthersAlone(t *testing.T) {
	other := errors.New("submitting the data generation job: 403 Forbidden")
	require.Equal(t, other, explainDataGenerationFailure(other, "my-agent"))

	systemErr := errors.New("Something went wrong during data generation")
	require.Equal(t, systemErr, explainDataGenerationFailure(systemErr, ""))

	require.NoError(t, explainDataGenerationFailure(nil, "my-agent"))
}
