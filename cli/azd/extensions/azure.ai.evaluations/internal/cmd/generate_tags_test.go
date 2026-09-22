// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
)

// Spec §5: a generated seed dataset records what it holds, so a later reattach
// or run does not have to infer it from row shape.
//
// The tag is not the request discriminator. Verified against a live job: a
// request carrying `options.type: simulation_seed` produced a version tagged
// `data_generation_type: conversation_simulation`. Writing the request spelling
// here would put the CLI's tag at odds with the service's own on one version.
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

// The request and the tag are two vocabularies for the same thing, and the
// service uses a different word in each. Asserting them together is what stops
// one being "corrected" to match the other.
//
// Live evidence, job datagen-774f6c7f31b449689898f01376f31932:
//
//	request  inputs.options.type      = "simulation_seed"
//	response result.outputs[0].tags   = { "data_generation_type": "conversation_simulation", ... }
func TestTheRequestTypeAndTheDatasetTagAreDifferentVocabularies(t *testing.T) {
	t.Parallel()

	const level = project.EvaluationLevelConversation

	assert.Equal(t, "simulation_seed", dataGenerationType(level),
		"the request carries the contract's DataGenerationJobType")
	assert.Equal(t, "conversation_simulation", datasetGenerationTag(level),
		"the tag carries what the service writes on the produced version")
	assert.NotEqual(t, dataGenerationType(level), datasetGenerationTag(level),
		"these are deliberately different; coupling them broke agreement with the service")

	// Turn level is the case where they do coincide, which is exactly why the
	// difference above is easy to miss.
	assert.Equal(t, "simple_qna", dataGenerationType(project.EvaluationLevelTurn))
	assert.Equal(t, "simple_qna", datasetGenerationTag(project.EvaluationLevelTurn))

	// Either spelling still reads as seeds, whichever side wrote it.
	assert.True(t, eval_api.SimulationSeedGenerationType(dataGenerationType(level)))
	assert.True(t, eval_api.SimulationSeedGenerationType(datasetGenerationTag(level)))
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

// The job resource echoes the submission, so the type it was generated with is
// readable straight off it. This is what a reattach uses instead of the plan it
// never saw, and it is the inverse of dataGenerationType.
func TestEvaluationLevelOfGeneration(t *testing.T) {
	t.Parallel()

	for _, level := range []string{project.EvaluationLevelConversation, project.EvaluationLevelTurn} {
		assert.Equal(t, level, evaluationLevelOfGeneration(dataGenerationType(level)),
			"the level survives the round trip through the submitted type")
	}

	// Anything else has no level to state. Defaulting to turn here would tag a
	// conversation dataset as a turn dataset the moment the service stopped
	// echoing inputs, which is worse than saying nothing.
	assert.Empty(t, evaluationLevelOfGeneration(""))
	assert.Empty(t, evaluationLevelOfGeneration("something_new"))
}

// The published contract is reported to name the simulation-seed shape
// `simulation_seed` rather than `conversation_simulation`. Which one a request
// The published contract names the simulation-seed shape `simulation_seed`;
// `conversation_simulation` is what this CLI sent before that landed, and what
// the portal writes into a version's tags. A reattach only ever *reads* the
// name, so it recognizes both -- a dataset tagged by the portal, or by an older
// build, must not come back with no level just because it spelled it
// differently.
func TestBothSimulationSeedSpellingsAreRecognizedOnTheWayBack(t *testing.T) {
	t.Parallel()

	for _, spelling := range []string{
		eval_api.DataGenerationTypeSimulationSeed,
		eval_api.DataGenerationTypeConversationSimulation,
	} {
		assert.True(t, eval_api.SimulationSeedGenerationType(spelling), "%q names seeds", spelling)
		assert.Equal(t, project.EvaluationLevelConversation, evaluationLevelOfGeneration(spelling))

		assert.True(t, conversationSeedDataset(&dataset_api.Dataset{
			Tags: map[string]string{"data_generation_type": spelling},
		}), "a version tagged %q holds seeds", spelling)

		// The portal writes the same value under `scenario`, so both keys are
		// read under both spellings.
		assert.True(t, conversationSeedDataset(&dataset_api.Dataset{
			Tags: map[string]string{"scenario": spelling},
		}))
	}

	// A turn dataset is still not a seed dataset under either spelling.
	assert.False(t, eval_api.SimulationSeedGenerationType(eval_api.DataGenerationTypeSimpleQnA))
	assert.False(t, eval_api.SimulationSeedGenerationType(""))
}

// GenerationType reads the submitted type without assuming the service echoed
// it. A job with no inputs is the older response shape, not a turn job.
func TestGenerationJobGenerationType(t *testing.T) {
	t.Parallel()

	withInputs := &eval_api.GenerationJob{
		ID: "datagen-1",
		Inputs: &eval_api.DataGenerationInputs{
			Options: eval_api.DataGenerationOptions{
				Type: eval_api.DataGenerationTypeConversationSimulation,
			},
		},
	}
	assert.Equal(t, "conversation_simulation", withInputs.GenerationType())

	assert.Empty(t, (&eval_api.GenerationJob{ID: "datagen-2"}).GenerationType(),
		"a response that echoed nothing states no type")
	assert.Empty(t, (*eval_api.GenerationJob)(nil).GenerationType())
}

// A standalone `generate --no-wait` has no azd environment to record the level
// in, so the reattach that finishes it has to recover the level from the job
// itself. Reading only the local note is what left such a version tagged with
// nothing.
func TestGenerationLevelForRecoversFromTheJobWithoutLocalState(t *testing.T) {
	t.Parallel()

	// No azd client at all: the standalone case, where there is nowhere a level
	// could have been recorded.
	ec := &evalContext{}
	ctx := context.Background()

	simulated := &eval_api.GenerationJob{
		ID: "datagen-1",
		Inputs: &eval_api.DataGenerationInputs{
			Options: eval_api.DataGenerationOptions{
				Type: eval_api.DataGenerationTypeConversationSimulation,
			},
		},
	}
	assert.Equal(t, project.EvaluationLevelConversation, ec.generationLevelFor(ctx, simulated))

	qna := &eval_api.GenerationJob{
		ID: "datagen-2",
		Inputs: &eval_api.DataGenerationInputs{
			Options: eval_api.DataGenerationOptions{Type: eval_api.DataGenerationTypeSimpleQnA},
		},
	}
	assert.Equal(t, project.EvaluationLevelTurn, ec.generationLevelFor(ctx, qna))

	// A service that echoes nothing falls through to local state, which is
	// empty here -- not to a default that would mislabel the version.
	assert.Empty(t, ec.generationLevelFor(ctx, &eval_api.GenerationJob{ID: "datagen-3"}))
	assert.Empty(t, ec.generationLevelFor(ctx, nil))
	assert.Empty(t, ec.generationLevelFor(ctx, &eval_api.GenerationJob{}),
		"a job with no id is nothing to recover against")
}
