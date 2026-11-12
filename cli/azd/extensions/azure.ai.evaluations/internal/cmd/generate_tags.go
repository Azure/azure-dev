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
//
// The tag is NOT the request discriminator. The service takes
// `options.type: simulation_seed` and then writes
// `data_generation_type: conversation_simulation` onto the version it produces
// -- verified against a live job. Writing the request spelling here would
// disagree with the service's own tag on the same version, and a reader has no
// way to tell which one is authoritative.
func seedDatasetTags(evaluationLevel string) map[string]string {
	if evaluationLevel == "" {
		return nil
	}
	return map[string]string{
		tagDataGenerationType: datasetGenerationTag(evaluationLevel),
		tagEvaluationLevel:    evaluationLevel,
		tagScenario:           scenarioEvaluation,
	}
}

// datasetGenerationTag is the value the service records on a generated version
// for this level, which is the value the CLI has to write to agree with it.
func datasetGenerationTag(evaluationLevel string) string {
	if evaluationLevel == project.EvaluationLevelConversation {
		return eval_api.DataGenerationTypeConversationSimulation
	}
	return eval_api.DataGenerationTypeSimpleQnA
}

// registeredEvaluationLevel prefers an explicit level, then the service's
// generation type, then the portal's scenario tag. Unknown tags imply no level.
func registeredEvaluationLevel(registered *dataset_api.Dataset) string {
	if registered == nil {
		return ""
	}
	if level := registered.Tags[tagEvaluationLevel]; level != "" {
		return level
	}
	if level := evaluationLevelOfGeneration(registered.Tags[tagDataGenerationType]); level != "" {
		return level
	}
	if conversationSeedDataset(registered) {
		return project.EvaluationLevelConversation
	}
	return ""
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
//
// The job the service holds is asked first. It echoes the submission back, so
// it states the type authoritatively and -- unlike the local record -- it is
// there whether or not the generation ran inside an azd environment. A
// standalone `generate --no-wait` has nowhere to write the local note, and
// reading only that note is what left the reattached version tagged with
// nothing and the declaration written at whatever a later reader assumed.
//
// Local state remains the fallback, for an older service that does not echo
// inputs.
func (ec *evalContext) generationLevelFor(ctx context.Context, job *eval_api.GenerationJob) string {
	if job == nil || job.ID == "" {
		return ""
	}
	if level := evaluationLevelOfGeneration(job.GenerationType()); level != "" {
		return level
	}
	return ec.privateValue(ctx, generationLevelKey(job.ID))
}

// evaluationLevelOfGeneration inverts dataGenerationType.
//
// Only the type that has a level maps to one. An unrecognized or absent type
// returns empty rather than defaulting to turn, so a service that stops echoing
// inputs falls through to the local record instead of overwriting a
// conversation dataset's tag with the wrong level.
//
// Both simulation-seed spellings are accepted: this reads what the service
// echoed, and a job submitted by the portal or by a later CLI may name the
// shape differently from the one this CLI sends.
func evaluationLevelOfGeneration(generationType string) string {
	if eval_api.SimulationSeedGenerationType(generationType) {
		return project.EvaluationLevelConversation
	}
	if generationType == eval_api.DataGenerationTypeSimpleQnA {
		return project.EvaluationLevelTurn
	}
	return ""
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
	current *dataset_api.Dataset,
	evaluationLevel string,
) {
	tags := seedDatasetTags(evaluationLevel)
	if len(tags) == 0 || current == nil || current.Name == "" || current.Version == "" || ec.datasetClient == nil {
		return
	}

	logger := azdext.NewLogger("eval.dataset_tags")

	merged := map[string]string{}
	maps.Copy(merged, current.Tags)
	maps.Copy(merged, tags)
	if tagsAlreadyApplied(current.Tags, tags) {
		return
	}
	dataURI := current.ResolvedBlobURI()
	if dataURI == "" {
		logger.Debug("could not record generation tags: version has no blob URI", "dataset", current.Name)
		return
	}
	if _, err := ec.datasetClient.FinalizeDatasetVersionTagged(
		ctx, current.Name, current.Version, dataURI, merged, ProjectEndpointAPIVersion,
	); err != nil {
		logger.Debug("could not record generation tags", "dataset", current.Name)
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
	if eval_api.SimulationSeedGenerationType(registered.Tags[tagDataGenerationType]) {
		return true
	}
	return eval_api.SimulationSeedGenerationType(registered.Tags[tagScenario])
}
