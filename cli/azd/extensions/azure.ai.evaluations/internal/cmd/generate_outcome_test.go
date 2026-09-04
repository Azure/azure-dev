// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two generations are independent, so one can succeed while the other
// fails, and the service has already billed the one that succeeded. Returning
// the failure before emitting the document left a JSON caller with neither the
// reference nor the job id for work they paid for, and no way to reattach.
func TestTheOutcomeDocumentSurvivesAPartialFailure(t *testing.T) {
	outcomes := []generationOutcome{
		{plan: generationPlan{Kind: generateKindDataset}, jobID: "job-42"},
		{plan: generationPlan{Kind: generateKindEvaluator}, err: assert.AnError},
	}

	doc := generationDocument(outcomes)

	raw, err := json.Marshal(doc)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	dataset, ok := got[string(generateKindDataset)].(map[string]any)
	require.True(t, ok, "the billed job has to be in the document")
	assert.Equal(t, "job-42", dataset["job_id"],
		"the job id is how the caller reattaches to work already paid for")

	assert.Contains(t, got, string(generateKindEvaluator),
		"the failed artifact is named too, so the caller can tell which is which")
	assert.Nil(t, got[string(generateKindEvaluator)])
}
