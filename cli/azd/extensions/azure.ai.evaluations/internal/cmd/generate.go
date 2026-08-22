// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// generatePollBudget replaces the inherited 2s x 300 (10 minute) client budget.
// The generation job is not gateway-capped; the old limit simply gave up while
// the service was still working, forcing a second command.
var generatePollBudget = eval_api.PollerOptions{
	Interval:    5 * time.Second,
	MaxAttempts: 720, // one hour
}

// generationPlan is everything one generation job needs, after the flags, the
// generation spec, and the eval's own target have been reconciled.
type generationPlan struct {
	// Name of the artifact being generated — the positional argument.
	Name string
	// Agent whose context seeds generation. May be empty, in which case
	// generation runs from the instruction alone.
	Agent string
	// Model deployment the generation job runs against.
	Model string
	// Instruction describing what the agent does and what to test.
	Instruction string
	// BaseDir is the directory OutputDir resolves against.
	BaseDir string
	// OutputDir is where the artifact is written.
	OutputDir string
	// SampleSize applies to dataset generation only.
	SampleSize int
	// From is what --from named: which of the service's sources to send. Empty
	// sends whatever the plan has to offer.
	From []string
	// TraceDays seeds generation from that many days of recent traces.
	TraceDays int
	// Kind is which artifact this plan produces, so one runner can submit both.
	Kind generateKind
}

// generateKind names the two generation resources, which share no collection.
type generateKind string

const (
	generateKindDataset   generateKind = "dataset"
	generateKindEvaluator generateKind = "evaluator"
)

// traceOptions converts the plan's trace window into the generation client's
// day count. Traces seed generation only; they are never a run's data source.
func (p generationPlan) traceOptions() *eval_api.TraceOptions {
	if p.TraceDays <= 0 {
		return nil
	}
	return &eval_api.TraceOptions{Days: p.TraceDays}
}

// resolveInstruction returns the generation instruction, reading it from a
// file when one is named.
//
// A useful instruction describes the agent and what to test, which is often
// more than fits comfortably on a command line, so it can live in a file that
// is reviewable alongside the rest of the config.
func resolveInstruction(inline, path string) (string, error) {
	if path == "" {
		return inline, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", messages.ReadingInstructionFile(path, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", messages.InstructionFileEmpty(path)
	}
	return text, nil
}

// declaredInstructions reads the file named by a generation entry's
// `instructions`, relative to the spec that declared it.
//
// A missing file is not an error. The path can be written before the file
// exists, so treating its absence as a failure would break the flow `init`
// scaffolds.
func declaredInstructions(named, configPath string) (string, error) {
	if named == "" {
		return "", nil
	}

	path := named
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), filepath.FromSlash(named))
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", messages.ReadingInstructions(named, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// resolveGenerationInstruction decides what generation is seeded from.
//
// The service accepts an agent source that is meant to pull the agent's own
// instructions, but it fails for every agent, so the agent's context is read
// here instead. In precedence order: what the caller passed, the instructions
// the project already holds, then the agent's published ones.
//
// The project comes before the service because a local read cannot fail
// slowly, and because instructions that have been optimized but not yet
// deployed are the ones the author means — generating against what is still
// published would test the version they are replacing.
//
// The last step is what makes `generate` work with no authored input at all,
// which is the flow `init` sets up.
func (ec *evalContext) resolveGenerationInstruction(
	ctx context.Context,
	explicit, agentName string,
	out io.Writer,
	quiet bool,
) (string, error) {
	if explicit != "" {
		return explicit, nil
	}

	if agentName == "" {
		return "", nil
	}

	local, path, err := ec.agentInstructionsFromProject(ctx, agentName)
	if err != nil {
		return "", err
	}
	if local != "" {
		if !quiet {
			fmt.Fprint(out, messages.SeedingFromFile(filepath.ToSlash(path)))
		}
		return local, nil
	}

	agent, err := ec.evalClient.GetAgent(ctx, agentName, ProjectEndpointAPIVersion)
	if err != nil {
		// Reported without stopping, because the model can still be supplied by
		// --generation-model and the caller has its own checks for what is left
		// missing. Making an absent agent fatal here reads well for a typo but
		// takes away the only path to "nothing supplied a model", which is the
		// case the flag validation exists for.
		if !quiet {
			fmt.Fprint(out, messages.WarningAgentUnreadable(agentName, err))
		}
		return "", nil
	}
	instructions := agent.Instructions()
	if instructions != "" && !quiet {
		fmt.Fprint(out, messages.SeedingFromAgent(agentName))
	}
	return instructions, nil
}

// agentInstructionsFromProject reads the agent's instructions out of the azd
// project, coming back empty when there is no project to read.
//
// Running outside a project is ordinary — the atomic commands work standalone
// against the data plane — so not finding one is not an error. An ambiguous
// target inside one is, because it would otherwise pick an agent at random.
func (ec *evalContext) agentInstructionsFromProject(
	ctx context.Context,
	agentName string,
) (instruction string, path string, err error) {
	if ec.azdClient == nil {
		return "", "", nil
	}
	resp, err := ec.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return "", "", nil
	}
	return project.AgentInstructionsFromProject(resp.GetProject(), agentName)
}

// generateRubric submits the evaluator generation job and saves the rubric.
func (ec *evalContext) generateRubric(
	ctx context.Context,
	plan generationPlan,
	out io.Writer,
	noWait bool,
	// jobID receives the submitted job's id, for the same reason as above.
	jobID *string,
) (*project.ArtifactRef, error) {
	fmt.Fprint(out, messages.GeneratingRubric(plan.Name))

	sources, unbuildable := eval_api.BuildGenerationSources(
		plan.From, plan.Agent, "", plan.Instruction, plan.traceOptions(),
	)
	if err := refuseUnusableSources(sources, unbuildable); err != nil {
		return nil, err
	}
	req := eval_api.NewEvaluatorGenerationJobRequest(plan.Name, plan.Model, sources)

	job, err := ec.evalClient.CreateEvaluatorGenerationJob(ctx, req, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, messages.SubmittingRubricJob(err)
	}
	if jobID != nil {
		*jobID = job.ID
	}
	if noWait {
		reportSubmitted(out, "evaluator", job.ID)
		return nil, nil
	}

	completed, err := ec.pollGeneration(ctx, job.ID, ProjectEndpointAPIVersion,
		ec.evalClient.GetEvaluatorGenerationJob)
	if err != nil {
		return nil, messages.RubricGeneration(err)
	}

	path := project.ArtifactPath(plan.BaseDir, plan.OutputDir, plan.Name, ".json")
	if err := writeRubric(path, completed.Result); err != nil {
		return nil, err
	}
	fmt.Fprint(out, messages.WroteArtifact(path))

	_, version := completed.ResolvedNameVersion()
	return &project.ArtifactRef{
		Name:    plan.Name,
		Source:  relativeSource(plan.BaseDir, path),
		Version: version,
	}, nil
}

// refuseUnbuildableSources reports a --from the plan could not honour.
//
// Submitting anyway would run a billed job seeded from less than was asked for
// and return a plausible-looking artifact, which is the worst outcome: the
// caller has no way to tell it apart from one built the way they intended.
// refuseUnusableSources rejects a generation the service could only refuse.
//
// Unbuildable kinds each get their own reason. Beyond those, a request with no
// sources at all is refused here rather than sent: a kind can be selected
// without being asked for and without anything to build it from, which added to
// neither list, so an empty request went out and came back as a 400 wrapping
// thirty lines of JSON around one sentence.
func refuseUnusableSources(sources []eval_api.GenerationSource, kinds []string) error {
	if err := refuseUnbuildableSources(kinds); err != nil {
		return err
	}
	if len(sources) == 0 {
		return messages.NothingToGenerateFrom()
	}
	return nil
}

func refuseUnbuildableSources(kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	reasons := map[string]string{
		"prompt": messages.FromPromptNeedsInstruction(),
		"agent":  messages.FromAgentNeedsTarget(),
		"file":   messages.FromFileNotASource(),
	}
	reasonsForKinds := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if reason, ok := reasons[k]; ok {
			reasonsForKinds = append(reasonsForKinds, reason)
			continue
		}
		reasonsForKinds = append(reasonsForKinds, messages.FromNotBuildable(k))
	}
	return messages.UnbuildableSources(reasonsForKinds)
}

// reportSubmitted says what was started and how to get back to it.
//
// The job id goes into the command rather than being left as a placeholder:
// --no-wait exists so the caller can walk away, and the line they walk away
// with has to be the one they can paste when they come back. The group is named
// too, because the two job types share no collection.
func reportSubmitted(out io.Writer, group, jobID string) {
	fmt.Fprint(out, messages.JobSubmitted(jobID))
	fmt.Fprint(out, messages.ReattachToJob(group, jobID))
}

// generateDataset submits the data generation job and downloads the result.
func (ec *evalContext) generateDataset(
	ctx context.Context,
	plan generationPlan,
	out io.Writer,
	noWait bool,
	// jobID receives the submitted job's id. Under --no-wait nothing is
	// downloaded and there is no artifact to return, so this is the only thing
	// the caller can report or reattach to.
	jobID *string,
) (*project.ArtifactRef, error) {
	fmt.Fprint(out, messages.GeneratingDataset(plan.Name, plan.SampleSize))

	sources, unbuildable := eval_api.BuildGenerationSources(
		plan.From, plan.Agent, "", plan.Instruction, plan.traceOptions(),
	)
	if err := refuseUnusableSources(sources, unbuildable); err != nil {
		return nil, err
	}
	req := eval_api.NewDataGenerationJobRequest(plan.Name, plan.Model, plan.SampleSize, sources)

	job, err := ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
	if err != nil {
		return nil, messages.SubmittingDataJob(err)
	}
	if jobID != nil {
		*jobID = job.ID
	}
	if noWait {
		reportSubmitted(out, "dataset", job.ID)
		return nil, nil
	}

	completed, err := ec.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
		ec.evalClient.GetDataGenerationJob)
	if err != nil && isAgentSeededGenerationFailure(err) {
		// Agent-seeded generation fails server-side for every agent, while the
		// same request carrying only the prompt succeeds. Failing the whole
		// command would block the documented flow on a defect the user cannot
		// do anything about, so retry without the agent and say so.
		promptOnly := eval_api.WithoutAgentSource(sources)
		if eval_api.HasPromptSource(promptOnly) {
			fmt.Fprint(out, messages.WarningAgentSeedFailedRetrying(plan.Agent))

			req = eval_api.NewDataGenerationJobRequest(
				plan.Name, plan.Model, plan.SampleSize, promptOnly)
			job, err = ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
			if err != nil {
				return nil, messages.SubmittingDataJob(err)
			}
			// The retry is a second billed job, so the id the caller reports
			// has to move with it. Leaving it on the abandoned first job points
			// every resume and every `job show` at the wrong one.
			if jobID != nil {
				*jobID = job.ID
			}
			completed, err = ec.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
				ec.evalClient.GetDataGenerationJob)
		}
	}
	if err != nil {
		return nil, messages.DataGeneration(explainDataGenerationFailure(err, plan.Agent))
	}

	name, version := completed.ResolvedNameVersion()
	if name == "" {
		return nil, messages.DataJobReturnedNoDataset()
	}

	// Confirm the version exists before reading it, so a missing dataset is
	// reported as such rather than as a download failure.
	if _, err := ec.datasetClient.GetDataset(
		ctx, name, version, ProjectEndpointAPIVersion,
	); err != nil {
		return nil, messages.ReadingGeneratedDataset(name, err)
	}
	content, err := ec.datasetClient.DownloadDatasetContent(ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, messages.DownloadingGeneratedDataset(name, err)
	}

	path := project.ArtifactPath(plan.BaseDir, plan.OutputDir, plan.Name, ".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, messages.Creating(filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, messages.Writing(path, err)
	}
	fmt.Fprint(out, messages.WroteArtifact(path))

	// The job registered the version and this file is a copy of it, so the
	// state a deploy would have left behind is recorded now. Without it the
	// next `azd up` finds no fingerprint for this dataset, reads the file as
	// new, and publishes a second version identical to the one just generated.
	ec.recordDeployedDataset(ctx, plan.Name, path, version)

	return &project.ArtifactRef{
		Name:    plan.Name,
		Source:  relativeSource(plan.BaseDir, path),
		Version: version,
	}, nil
}

// isAgentSeededGenerationFailure recognizes the service-side failure that hits
// every agent, so it can be retried without the agent rather than surfaced.
func isAgentSeededGenerationFailure(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "DataGenerationJobSystemError") ||
		strings.Contains(text, "Something went wrong during data generation")
}

// explainDataGenerationFailure adds context to the service's opaque system
// error.
//
// Seeding generation from an agent currently fails server-side with
// DataGenerationJobSystemError for every agent, within seconds, while the same
// request without the agent source runs normally. The raw message says only
// that something went wrong and to try again, which sends users into a retry
// loop against a deterministic failure.
func explainDataGenerationFailure(err error, agentName string) error {
	if err == nil || agentName == "" {
		return err
	}
	// The poller surfaces the service's message; the code is not always in it.
	text := err.Error()
	if !strings.Contains(text, "DataGenerationJobSystemError") &&
		!strings.Contains(text, "Something went wrong during data generation") {
		return err
	}
	return messages.AgentSeededGenerationFailing(err, agentName)
}

// pollGeneration waits for a generation job using the raised budget.
func (ec *evalContext) pollGeneration(
	ctx context.Context,
	operationID, apiVersion string,
	get eval_api.GetJobFunc,
) (*eval_api.GenerationJob, error) {
	poller := eval_api.NewPoller(operationID, apiVersion, get)
	poller.Options = generatePollBudget
	return poller.Poll(ctx)
}

// writeRubric persists the rubric so the developer can edit weights and
// descriptions and publish a new version.
//
// The definition is written through as it arrived rather than re-marshalled
// from a struct. Re-marshalling keeps only the fields the struct models, and
// dropped pass_threshold: the file then differed from the version that had just
// been published, so the next deploy republished it, silently without a
// threshold. Anything the service adds later would have been lost the same way.
func writeRubric(path string, result json.RawMessage) error {
	if len(result) == 0 {
		return messages.RubricJobReturnedNoResult()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return messages.Creating(filepath.Dir(path), err)
	}

	var envelope struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(result, &envelope); err == nil && len(envelope.Definition) > 0 {
		var probe struct {
			Dimensions []json.RawMessage `json:"dimensions"`
		}
		if json.Unmarshal(envelope.Definition, &probe) == nil && len(probe.Dimensions) > 0 {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, envelope.Definition, "", "  "); err != nil {
				return messages.Serializing(path, err)
			}
			return os.WriteFile(path, pretty.Bytes(), 0o600)
		}
	}

	// Fall back to the raw payload rather than losing the result.
	return os.WriteFile(path, result, 0o600)
}

// relativeSource expresses an artifact path relative to the deployment spec.
func relativeSource(baseDir, path string) string {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return "./" + filepath.ToSlash(rel)
}
