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
	"slices"
	"sort"
	"strings"
	"time"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
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
	// EvaluationLevel is what one generated row is: a turn, or a seed for a
	// simulated conversation. Dataset generation only.
	EvaluationLevel string
	// InstructionSource says where Instruction came from, for the reader. Empty
	// when nothing supplied one, which is a fact worth printing on its own.
	InstructionSource string
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
	// #nosec G304 -- path is the file the caller named on the command line.
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
	// #nosec G304 -- named comes from the eval config, resolved against it.
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", messages.ReadingInstructions(named, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// resolveGenerationInstruction decides what generation is seeded from, and
// says where it came from so the caller can report it.
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
	cmd *cobra.Command,
	explicit, explicitSource, agentName string,
	out io.Writer,
	quiet bool,
) (instruction string, source string, err error) {
	if explicit != "" {
		return explicit, explicitSource, nil
	}

	ctx := cmd.Context()
	if agentName != "" {
		local, path, err := ec.agentInstructionsFromProject(ctx, agentName)
		if err != nil {
			return "", "", err
		}
		if local != "" {
			return local, messages.InstructionSourceFile(path), nil
		}

		// The name reaching here can be an azure.yaml service key, which is what
		// `init` writes into a target and what --target accepts. The service knows
		// the agent by the name it publishes under, so asking for the key returned
		// nothing and the caller was warned about an agent that does exist. The
		// local lookup above takes either form; this one does not.
		remoteName, err := ec.remoteAgentName(ctx, agentName)
		if err != nil {
			return "", "", err
		}

		agent, err := ec.evalClient.GetAgent(ctx, remoteName, ProjectEndpointAPIVersion)
		if err != nil {
			// Reported without stopping, because the prompt below can still
			// supply what the agent would have.
			if !quiet {
				fmt.Fprint(out, messages.WarningAgentUnreadable(agentName, err))
			}
		} else if instructions := agent.Instructions(); instructions != "" {
			return instructions, messages.InstructionSourceAgent(), nil
		}
	}

	// Nothing detected. Generation seeded from nothing produced an evaluator the
	// service marked input_quality, so this is asked rather than shrugged at:
	// the caller knows what the agent is for, and one sentence is the whole
	// difference between a usable rubric and a billed job that grades noise.
	fmt.Fprint(out, messages.InstructionsNotDetected())
	if noPrompt(cmd) {
		return "", "", messages.InstructionsRequired()
	}
	typed, err := promptAgentInstruction(cmd)
	if err != nil {
		return "", "", err
	}
	return typed, messages.InstructionSourceTyped(), nil
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
// generationReport is what the caller learns about a job besides its artifact.
//
// Both fields outlive the artifact: under --no-wait there is no artifact at all
// and the id is the only thing to reattach to, and a warning has to reach the
// JSON document even though it is not part of what was produced.
type generationReport struct {
	jobID    string
	warnings []eval_api.JobWarning
}

func (ec *evalContext) generateRubric(
	ctx context.Context,
	plan generationPlan,
	out io.Writer,
	noWait bool,
	report *generationReport,
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
	report.record(job.ID)
	if noWait {
		reportSubmitted(out, "evaluator", job.ID)
		return nil, nil
	}

	completed, err := ec.pollGeneration(ctx, job.ID, ProjectEndpointAPIVersion,
		ec.evalClient.GetEvaluatorGenerationJob)
	if err != nil {
		return nil, messages.RubricGeneration(err)
	}
	report.warn(completed)

	// generate settled this up front with refuseExistingArtifact, so by here it
	// either found nothing or the caller passed --force.
	return ec.collectRubric(completed, plan.Name, plan.BaseDir, plan.OutputDir, out, true)
}

// record remembers the job the caller can reattach to.
func (r *generationReport) record(jobID string) {
	if r != nil {
		r.jobID = jobID
	}
}

// warn remembers what the service said about a job it completed.
func (r *generationReport) warn(job *eval_api.GenerationJob) {
	if r != nil && job != nil {
		r.warnings = job.AllWarnings()
	}
}

// collectRubric writes a finished evaluator job's rubric and describes it.
//
// Split from the submit so `job show` can finish a generation started with
// --no-wait: the artifact is what the job was billed for, and the client that
// submitted it is long gone.
func (ec *evalContext) collectRubric(
	completed *eval_api.GenerationJob,
	name, baseDir, outputDir string,
	out io.Writer,
	replaceExisting bool,
) (*project.ArtifactRef, error) {
	resolvedName, version := completed.ResolvedNameVersion()
	if name == "" {
		name = resolvedName
	}
	if name == "" {
		return nil, messages.RubricJobReturnedNoName()
	}
	// Reattaching names the file after whatever the job reports, and that name
	// has not been through the check the submit path applies. A returned
	// `../../config` would otherwise be written outside the output directory,
	// over whatever is already there.
	if !nameIsAPathComponent(name) {
		return nil, messages.ServiceNameNotAFileName("evaluator", name)
	}

	path := project.ArtifactPath(baseDir, outputDir, name, ".json")
	// A rubric is meant to be edited -- that is what the local file is for -- and
	// `job show` is documented as safe to re-run while polling. Collecting again
	// over an edited file made those two claims contradict each other.
	if !replaceExisting && artifactAlreadyCollected(path) {
		fmt.Fprint(out, messages.ArtifactLeftAlone(path))
		return &project.ArtifactRef{
			Name:    name,
			Source:  relativeSource(baseDir, path),
			Version: version,
		}, nil
	}
	if err := writeRubric(path, completed.Result); err != nil {
		return nil, err
	}
	fmt.Fprint(out, messages.WroteArtifact(path))
	writeJobWarnings(out, "evaluator", completed, path)

	return &project.ArtifactRef{
		Name:    name,
		Source:  relativeSource(baseDir, path),
		Version: version,
	}, nil
}

// writeJobWarnings reports what the service said about a job it completed.
//
// Printed beside the artifact it qualifies rather than collected at the end,
// because a run that generated two things has to say which one to look at. A
// warned job used to be reported as an unqualified success, and the artifact
// went into the configuration with nothing saying to read it first.
func writeJobWarnings(out io.Writer, kind string, job *eval_api.GenerationJob, path string) {
	for _, w := range job.AllWarnings() {
		fmt.Fprint(out, messages.GenerationWarning(kind, w.Code, w.Message, path))
	}
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
// retryConsent answers whether to submit the fallback generation job. It bills
// a second job against sources the caller did not ask for, so it is a question
// rather than a recovery.
type retryConsent func(agent, jobID string, why error) (bool, error)

func (ec *evalContext) generateDataset(
	ctx context.Context,
	plan generationPlan,
	out io.Writer,
	noWait bool,
	report *generationReport,
	consent retryConsent,
) (*project.ArtifactRef, error) {
	fmt.Fprint(out, messages.GeneratingDataset(plan.Name, plan.SampleSize))

	sources, unbuildable := eval_api.BuildGenerationSources(
		plan.From, plan.Agent, "", plan.Instruction, plan.traceOptions(),
	)
	if err := refuseUnusableSources(sources, unbuildable); err != nil {
		return nil, err
	}

	// Agent-seeded generation fails server-side for every agent, and the waiting
	// path below answers that with a prompt-only retry. Nothing retries under
	// --no-wait -- the client is gone by then -- so submitting the shape that is
	// known to fail would bill a job that cannot succeed and leave `job show`
	// reporting it. The shape that works is the one submitted.
	if noWait {
		if promptOnly := eval_api.WithoutAgentSource(sources); len(promptOnly) != len(sources) &&
			eval_api.HasPromptSource(promptOnly) {
			fmt.Fprint(out, messages.WarningAgentSeedSkippedAsync(plan.Agent))
			sources = promptOnly
		}
	}
	req := eval_api.NewDataGenerationJobRequest(plan.Name, plan.Model, plan.SampleSize, sources)

	job, err := ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
	if err != nil {
		return nil, messages.SubmittingDataJob(err)
	}
	report.record(job.ID)
	if noWait {
		reportSubmitted(out, "dataset", job.ID)
		return nil, nil
	}

	completed, err := ec.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
		ec.evalClient.GetDataGenerationJob)
	if err != nil && isAgentSeededGenerationFailure(err) {
		// Agent-seeded generation fails server-side for every agent, while the
		// same request carrying only the prompt succeeds. Retrying changes the
		// sources that were asked for and bills a second job, so it is asked
		// about rather than done: the command had already been confirmed for one
		// job against one set of sources.
		promptOnly := eval_api.WithoutAgentSource(sources)
		if eval_api.HasPromptSource(promptOnly) {
			retry, askErr := consent(plan.Agent, job.ID, err)
			if askErr != nil {
				return nil, askErr
			}
			if !retry {
				return nil, messages.AgentSeedFailedNoRetry(plan.Agent, job.ID)
			}
			fmt.Fprint(out, messages.RetryingWithPromptSource())

			req = eval_api.NewDataGenerationJobRequest(
				plan.Name, plan.Model, plan.SampleSize, promptOnly)
			job, err = ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
			if err != nil {
				return nil, messages.SubmittingDataJob(err)
			}
			reportSubmitted(out, "dataset", job.ID)
			// The retry is a second billed job, so the id the caller reports
			// has to move with it. Leaving it on the abandoned first job points
			// every resume and every `job show` at the wrong one.
			report.record(job.ID)
			completed, err = ec.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
				ec.evalClient.GetDataGenerationJob)
		}
	}
	if err != nil {
		return nil, messages.DataGeneration(explainDataGenerationFailure(err, plan.Agent))
	}
	report.warn(completed)

	// As above: the destination was checked before the job was submitted.
	return ec.collectDataset(ctx, completed, plan.Name, plan.BaseDir, plan.OutputDir, out, true)
}

// collectDataset downloads a finished data job's dataset and records what a
// deploy would have recorded.
//
// Split from the submit so `job show` can finish a generation started with
// --no-wait: the rows are what the job was billed for, and the client that
// submitted it is long gone.
func (ec *evalContext) collectDataset(
	ctx context.Context,
	completed *eval_api.GenerationJob,
	declaredName, baseDir, outputDir string,
	out io.Writer,
	replaceExisting bool,
) (*project.ArtifactRef, error) {
	name, version := completed.ResolvedNameVersion()
	if name == "" {
		return nil, messages.DataJobReturnedNoDataset()
	}
	// The path is named for what the author asked for, which is what the
	// catalog entry and any later regeneration use. Reattaching has only the
	// job, so the service's own name stands in.
	localName := declaredName
	if localName == "" {
		localName = name
	}
	// Checked before the download rather than after: the service's own name has
	// not been through the submit path's check, and a returned `../../rows`
	// would be written outside the output directory over whatever is there.
	if !nameIsAPathComponent(localName) {
		return nil, messages.ServiceNameNotAFileName("dataset", localName)
	}

	// Before the download, not after: re-running `job show` while polling should
	// cost nothing and must not write over rows somebody has since edited.
	if !replaceExisting {
		if path := project.ArtifactPath(baseDir, outputDir, localName, ".jsonl"); artifactAlreadyCollected(path) {
			fmt.Fprint(out, messages.ArtifactLeftAlone(path))
			return &project.ArtifactRef{
				Name:    localName,
				Source:  relativeSource(baseDir, path),
				Version: version,
			}, nil
		}
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

	path := project.ArtifactPath(baseDir, outputDir, localName, ".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, messages.Creating(filepath.Dir(path), err)
	}
	// Atomic, because regenerating writes over the dataset already sitting
	// there: os.WriteFile truncates first, so a failure mid-write destroys the
	// copy the caller had while still reporting the generation as failed.
	if err := writeFileAtomic(path, content); err != nil {
		return nil, err
	}
	fmt.Fprint(out, messages.WroteArtifact(path))
	writeJobWarnings(out, "dataset", completed, path)

	// The job registered the version and this file is a copy of it, so the
	// state a deploy would have left behind is recorded now. Without it the
	// next `azd up` finds no fingerprint for this dataset, reads the file as
	// new, and publishes a second version identical to the one just generated.
	ec.recordDeployedDataset(ctx, localName, path, version)

	return &project.ArtifactRef{
		Name:    localName,
		Source:  relativeSource(baseDir, path),
		Version: version,
	}, nil
}

// artifactAlreadyCollected reports a destination a previous collection filled.
//
// Only a file that is there. An unreadable one is treated as absent so the
// collection goes ahead and fails on the write, which says more than refusing
// on a stat would.
func artifactAlreadyCollected(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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
		if editable, ok := editableRubric(envelope.Definition); ok {
			return writeFileAtomic(path, editable)
		}
	}

	// Fall back to the raw payload rather than losing the result.
	return writeFileAtomic(path, result)
}

// rubricOwnedByTheService names the keys a reader cannot usefully edit.
//
// init_parameters, metrics and data_schema are the service's description of how
// the evaluator is wired, and prompt_text on a rubric is generated from the
// dimensions rather than authored. Left in the file they outnumbered the
// dimensions several times over, so the one thing this artifact exists to be
// edited for was the hardest part of it to find.
var rubricOwnedByTheService = []string{
	"init_parameters", "initParameters",
	"metrics",
	"data_schema", "dataSchema",
	"prompt_text", "promptText",
}

// editableRubric reduces a returned rubric to the part worth editing.
//
// It reports false for anything that is not a rubric, so a payload this does
// not understand is written whole rather than filtered down to nothing: losing
// a generated artifact is far worse than a wide one.
func editableRubric(definition json.RawMessage) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(definition, &fields); err != nil {
		return nil, false
	}
	var probe struct {
		Dimensions []json.RawMessage `json:"dimensions"`
	}
	if json.Unmarshal(definition, &probe) != nil || len(probe.Dimensions) == 0 {
		return nil, false
	}
	for _, key := range rubricOwnedByTheService {
		delete(fields, key)
	}

	// Ordered, because this file is committed and read in diffs: Go ranges maps
	// at random, so marshalling the map directly rewrote the whole rubric on
	// every regeneration whether or not anything about it had changed.
	pretty, err := json.MarshalIndent(orderedJSON(fields), "", "  ")
	if err != nil {
		return nil, false
	}
	return append(pretty, '\n'), true
}

// orderedJSON marshals a decoded object with its keys in a fixed order.
//
// The rubric's own three come first, in the order someone reads them, and
// anything the service adds later follows in sorted order rather than being
// dropped.
type orderedJSON map[string]json.RawMessage

func (o orderedJSON) MarshalJSON() ([]byte, error) {
	leading := []string{"type", "dimensions", "pass_threshold", "passThreshold"}
	rest := make([]string, 0, len(o))
	for key := range o {
		if !slices.Contains(leading, key) {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)

	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	write := func(key string) {
		raw, ok := o[key]
		if !ok {
			return
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		name, err := json.Marshal(key)
		if err != nil {
			return
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(raw)
	}
	for _, key := range leading {
		write(key)
	}
	for _, key := range rest {
		write(key)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// relativeSource expresses an artifact path relative to the deployment spec.
func relativeSource(baseDir, path string) string {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return "./" + filepath.ToSlash(rel)
}
