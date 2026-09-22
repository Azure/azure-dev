// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"maps"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
)

// Spec §5: a generated seed dataset records what it holds, so a later reattach
// or run does not have to infer it from row shape.
func TestSeedDatasetTags(t *testing.T) {
	t.Parallel()

	conversation := seedDatasetTags(project.EvaluationLevelConversation)
	assert.Equal(t, map[string]string{
		"data_generation_type": "conversation_simulation",
		"evaluation_level":     "conversation",
		"scenario":             "evaluation",
	}, conversation)

	turn := seedDatasetTags(project.EvaluationLevelTurn)
	assert.Equal(t, "simple_qna", turn["data_generation_type"],
		"a turn dataset says so rather than saying nothing")
	assert.Equal(t, "turn", turn["evaluation_level"])

	assert.Nil(t, seedDatasetTags(""),
		"an unstated level tags nothing rather than asserting a default nobody asked for")
}

// The level a reattach recovers comes from the version's own tags, which is the
// only thing `job show` has to read: it has no plan.
func TestRegisteredEvaluationLevel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "conversation", registeredEvaluationLevel(&dataset_api.Dataset{
		Tags: map[string]string{"evaluation_level": "conversation"},
	}))
	assert.Empty(t, registeredEvaluationLevel(&dataset_api.Dataset{}))
	assert.Empty(t, registeredEvaluationLevel(nil))
}

// A plan states the level; reattaching has none. Taking the plan first keeps a
// regeneration authoritative over whatever an older version happened to carry.
func TestEvaluationLevelForRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		planLevel string
		ref       *project.ArtifactRef
		want      string
	}{
		{
			name:      "the plan wins when it states a level",
			planLevel: "conversation",
			ref:       &project.ArtifactRef{EvaluationLevel: "turn"},
			want:      "conversation",
		},
		{
			name:      "a reattach falls back to what the version recorded",
			planLevel: "",
			ref:       &project.ArtifactRef{EvaluationLevel: "conversation"},
			want:      "conversation",
		},
		{
			name:      "neither states one",
			planLevel: "",
			ref:       &project.ArtifactRef{},
			want:      "",
		},
		{
			name:      "no ref at all",
			planLevel: "",
			ref:       nil,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, evaluationLevelForRef(tt.planLevel, tt.ref))
		})
	}
}

// Spec §5 allows the portal's own spelling as well. Neither tag proves the rows
// are usable -- row shape is still validated before a run.
func TestConversationSeedDataset_RecognizesBothSpellings(t *testing.T) {
	t.Parallel()

	assert.True(t, conversationSeedDataset(&dataset_api.Dataset{
		Tags: map[string]string{"data_generation_type": "conversation_simulation"},
	}), "what the CLI writes")

	assert.True(t, conversationSeedDataset(&dataset_api.Dataset{
		Tags: map[string]string{"scenario": "conversation_simulation"},
	}), "what the portal writes")

	assert.False(t, conversationSeedDataset(&dataset_api.Dataset{
		Tags: map[string]string{"data_generation_type": "simple_qna", "scenario": "evaluation"},
	}), "a turn dataset is not a seed dataset")

	assert.False(t, conversationSeedDataset(&dataset_api.Dataset{}))
	assert.False(t, conversationSeedDataset(nil))
}

// The service records the generation job's id in the same tag map, and
// UpdateVersionTags sends the map as given. Merging is what stops the CLI
// erasing provenance it did not write.
func TestGeneratedTagsMergeRatherThanReplace(t *testing.T) {
	t.Parallel()

	serviceWrote := map[string]string{
		"generation_job_id": "job_abc123",
		"something_else":    "preserved",
	}
	ours := seedDatasetTags(project.EvaluationLevelConversation)

	merged := map[string]string{}
	maps.Copy(merged, serviceWrote)
	maps.Copy(merged, ours)

	assert.Equal(t, "job_abc123", merged["generation_job_id"],
		"the job id is how a dataset says where it came from")
	assert.Equal(t, "preserved", merged["something_else"])
	assert.Equal(t, "conversation", merged["evaluation_level"])
	assert.Len(t, merged, 5)
}
