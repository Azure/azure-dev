// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Tags a generated dataset carries so anything reading it later can tell what
// it holds without parsing its rows.
//
// `job show` reattaches to a job with no plan to consult, and a run has to know
// whether rows are scenario seeds or query/response pairs. Row shape alone is
// ambiguous enough to guess wrong, and a guess writes the wrong declaration.
const (
	tagDataGenerationType = "data_generation_type"
	tagEvaluationLevel    = "evaluation_level"
	tagScenario           = "scenario"

	scenarioEvaluation = "evaluation"
)

// seedDatasetTags is what a generated dataset should carry for the level it was
// generated at. An unstated level tags nothing rather than asserting a default
// the caller never asked for.
func seedDatasetTags(evaluationLevel string) map[string]string {
	if evaluationLevel == "" {
		return nil
	}
	return map[string]string{
		tagDataGenerationType: dataGenerationType(evaluationLevel),
		tagEvaluationLevel:    evaluationLevel,
		tagScenario:           scenarioEvaluation,
	}
}

// registeredEvaluationLevel reads the level a dataset version records.
func registeredEvaluationLevel(registered *dataset_api.Dataset) string {
	if registered == nil {
		return ""
	}
	return registered.Tags[tagEvaluationLevel]
}

// generationLevelKey records what level a generation job was asked for, against
// the job's own id.
//
// Written at submission rather than on completion, because `--no-wait` returns
// as soon as the job is accepted and there is no dataset to tag yet. `job show`
// is what finishes such a generation, and it arrives with a job id and nothing
// else: without this the level is gone, the version is tagged with nothing, and
// the reattached declaration is written at whatever a reader assumes.
//
// Local state rather than the job resource, so it does not depend on the
// service echoing back a field it is not contracted to return.
func generationLevelKey(jobID string) string {
	return project.FingerprintKey("data_job", jobID) + "_LEVEL"
}

// rememberGenerationLevel records the level a submitted job was asked for.
//
// Best effort, like every other write to this state: a job that was accepted is
// not un-submitted by an environment that could not be written, and the only
// cost of losing it is the untagged version this exists to prevent.
func (ec *evalContext) rememberGenerationLevel(ctx context.Context, jobID, evaluationLevel string) {
	if jobID == "" || evaluationLevel == "" {
		return
	}
	ec.remember(ctx, generationLevelKey(jobID), evaluationLevel)
}

// generationLevelFor is what a reattach knows about a job it did not wait for.
func (ec *evalContext) generationLevelFor(ctx context.Context, jobID string) string {
	if jobID == "" {
		return ""
	}
	return ec.privateValue(ctx, generationLevelKey(jobID))
}

// applyGeneratedDatasetTags records on the registered version what was
// generated, so a later reattach or run does not have to infer it.
//
// Best effort: the dataset exists and its rows are already on disk, so failing
// the command over a tag would discard work that succeeded. The level is still
// written to the local catalog either way.
//
// Existing tags are merged rather than replaced. The service records the
// generation job's own id here, and UpdateVersionTags sends the map as given --
// so passing only these three would erase the provenance the service wrote.
func (ec *evalContext) applyGeneratedDatasetTags(
	ctx context.Context,
	ref *project.ArtifactRef,
	evaluationLevel string,
) {
	tags := seedDatasetTags(evaluationLevel)
	if len(tags) == 0 || ref == nil || ref.Name == "" || ref.Version == "" || ec.datasetClient == nil {
		return
	}

	logger := azdext.NewLogger("eval.dataset_tags")

	current, err := ec.datasetClient.GetDataset(ctx, ref.Name, ref.Version, ProjectEndpointAPIVersion)
	if err != nil {
		logger.Debug("could not read the version's current tags", "dataset", ref.Name)
		return
	}

	merged := map[string]string{}
	maps.Copy(merged, current.Tags)
	maps.Copy(merged, tags)
	if tagsAlreadyApplied(current.Tags, tags) {
		return
	}

	if _, err := ec.datasetClient.UpdateVersionTags(
		ctx, ref.Name, ref.Version, merged, ProjectEndpointAPIVersion,
	); err != nil {
		logger.Debug("could not record generation tags", "dataset", ref.Name)
	}
}

// evaluationLevelForRef prefers what this run asked for, and falls back to what
// the registered version says.
//
// A plan states the level; reattaching has none. Taking the plan first keeps a
// regeneration authoritative over whatever an older version happened to carry.
func evaluationLevelForRef(planLevel string, ref *project.ArtifactRef) string {
	if planLevel != "" {
		return planLevel
	}
	if ref != nil {
		return ref.EvaluationLevel
	}
	return ""
}

// conversationSeedDataset reports whether a registered version says it holds
// scenario seeds.
//
// The tag is what the CLI writes; the portal writes scenario:
// conversation_simulation for the same thing, so both are recognized. Neither
// is proof the rows are usable -- row shape is still validated before a run.
func conversationSeedDataset(registered *dataset_api.Dataset) bool {
	if registered == nil {
		return false
	}
	if registered.Tags[tagDataGenerationType] == eval_api.DataGenerationTypeConversationSimulation {
		return true
	}
	return registered.Tags[tagScenario] == eval_api.DataGenerationTypeConversationSimulation
}
