// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/require"
)

// The create request has no description field, so a documented description
// would otherwise be parsed and dropped.
func TestBuildCarriesGroupDescriptionInMetadata(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name"}, "turn"),
	}
	group := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.similarity"}), "")
	group.Description = "Quality gate for the support agent"

	req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.NoError(t, err)
	require.Equal(t, "Quality gate for the support agent", req.Metadata["azd_description"])
}

// An absent description adds no metadata key rather than an empty one.
func TestBuildOmitsEmptyDescription(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name"}, "turn"),
	}
	group := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.similarity"}), "")

	req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.NoError(t, err)
	require.NotContains(t, req.Metadata, "azd_description")
}
