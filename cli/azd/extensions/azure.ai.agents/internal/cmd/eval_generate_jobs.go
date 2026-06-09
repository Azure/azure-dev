// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// eval_init_jobs.go handles generation job submission and polling for the
// eval generate command. It submits dataset and evaluator generation requests,
// polls for completion in parallel, downloads artifacts on success, and
// persists state for resume on timeout.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/fatih/color"
)

// resolveEvalName returns the eval suite name from flags, falling back to defaultEvalName.
func resolveEvalName(flags *evalGenerateFlags) string {
	if flags.name != "" {
		return flags.name
	}
	return defaultEvalName
}

// resolvedInstruction returns the instruction content from flags, reading
// from file if instructionFile is set.
func resolvedInstruction(flags *evalGenerateFlags) string {
	if flags.instructionFile != "" {
		data, err := os.ReadFile(flags.instructionFile) //nolint:gosec // user-provided path validated earlier
		if err != nil {
			return flags.instruction
		}
		return string(data)
	}
	return flags.instruction
}

// newEvalConfig builds an evalConfig from flags and resolved context, applying defaults as needed.
func newEvalConfig(flags *evalGenerateFlags, resolved *evalResolvedContext) *evalConfig {
	agent := evalAgentRef{
		Name: resolved.agentName,
		Kind: resolved.agentKind,
		// Version is intentionally omitted — it is resolved at run time
		// from the azd environment (AGENT_{SVC}_VERSION) so eval.yaml
		// never contains a stale version that drifts after redeployment.
	}
	if flags.configFile != "" {
		agent.ConfigFile = flags.configFile
	}
	if flags.instruction != "" {
		agent.Instruction.Value = flags.instruction
	}
	if flags.instructionFile != "" {
		agent.Instruction.File = flags.instructionFile
	}
	return &evalConfig{
		Config: opt_eval.Config{
			Name:  resolveEvalName(flags),
			Agent: agent,
		},
		Options: &opt_eval.Options{
			EvalModel: flags.evalModel,
		},
		MaxSamples: flags.maxSamples,
		TraceDays:  flags.traceDays,
	}
}

// submitDatasetGeneration submits a dataset generation job and returns the created job or an error.
func submitDatasetGeneration(
	ctx context.Context,
	resolved *evalResolvedContext,
	flags *evalGenerateFlags,
) (*eval_api.GenerationJob, error) {
	// Traces are only supported for evaluator generation, not dataset generation.
	prompt := resolvedInstruction(flags)
	sources := eval_api.BuildGenerationSources(
		string(resolved.agentKind), resolved.agentName, resolved.version, prompt, nil,
	)
	request := eval_api.NewDataGenerationJobRequest(
		resolveEvalName(flags), flags.evalModel, flags.maxSamples, sources,
	)
	return resolved.evalClient.CreateDataGenerationJob(ctx, request, DataGenerationAPIVersion)
}

// submitEvaluatorGeneration submits an evaluator generation job and returns the created job or an error.
func submitEvaluatorGeneration(
	ctx context.Context,
	resolved *evalResolvedContext,
	flags *evalGenerateFlags,
) (*eval_api.GenerationJob, error) {
	var traces *eval_api.TraceOptions
	if flags.traceDays > 0 {
		traces = &eval_api.TraceOptions{Days: flags.traceDays}
	}
	prompt := resolvedInstruction(flags)
	sources := eval_api.BuildGenerationSources(
		string(resolved.agentKind), resolved.agentName, resolved.version, prompt, traces,
	)
	request := eval_api.NewEvaluatorGenerationJobRequest(
		resolveEvalName(flags), flags.evalModel, sources,
	)
	return resolved.evalClient.CreateEvaluatorGenerationJob(ctx, request, ProjectEndpointAPIVersion)
}

// resolveCwdRelative converts a relative path to an absolute path based on
// the current working directory. Already-absolute paths are returned as-is.
// This should be called on CLI flag values before passing them to
// resolveLocalDatasetFile, which resolves against the agent project directory.
func resolveCwdRelative(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// resolveLocalDatasetFile resolves the dataset flag value to an absolute path
// for the local JSONL file. If the value is relative it is resolved against
// the agent project directory.
func resolveLocalDatasetFile(dataset string, agentProject string) (string, error) {
	if filepath.IsAbs(dataset) {
		if _, err := os.Stat(dataset); err != nil {
			return "", fmt.Errorf("dataset file %q is not accessible: %w", dataset, err)
		}
		return dataset, nil
	}
	abs := filepath.Join(agentProject, dataset)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("dataset file %q is not accessible: %w", dataset, err)
	}
	return abs, nil
}

func datasetFromJob(job *eval_api.GenerationJob) *evalDatasetRef {
	name, version := job.ResolvedNameVersion()
	if name == "" {
		return nil
	}
	return &evalDatasetRef{
		Name:    name,
		Version: version,
	}
}

func evaluatorFromJob(job *eval_api.GenerationJob) (string, string) {
	return job.ResolvedNameVersion()
}

func evaluatorsFromFlags(values []string) opt_eval.EvaluatorList {
	refs := make(opt_eval.EvaluatorList, len(values))
	for i, v := range values {
		refs[i] = opt_eval.EvaluatorRef{Name: v}
	}
	return refs
}

func buildOpenAIEvalRequest(evalCfg *evalConfig) *eval_api.CreateOpenAIEvalRequest {
	return evalCfg.ToAgentTargetAdaptableEvalGroupRequest()
}

// resumeEvalGenerate handles resuming an eval generate when generation jobs are still pending. It polls for job completion, updates state and config on success, and persists state for later resume if polling times out.
func resumeEvalGenerate(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *opt_eval.EvalState,
) error {
	if _, err := pollAndFinalizeJobs(ctx, resolved, evalCfg, state, nil); err != nil {
		if _, ok := errors.AsType[*initTimeoutError](err); ok {
			return writeTimedOutEvalGenerate(ctx, resolved, configPath, evalCfg, state)
		}
		return err
	}
	state.InitStatus = opt_eval.InitStatusCompleted
	if err := opt_eval.ClearEvalState(ctx, resolved.azdClient, resolved.envName); err != nil {
		log.Printf("warning: clearing eval state: %v", err)
	}
	if resolved.hasProject {
		if err := eval_api.WriteEvalReviewArtifacts(resolved.agentProject, evalCfg); err != nil {
			log.Printf("warning: writing eval review artifacts: %v", err)
		}
	}
	return eval_api.WriteEvalConfig(configPath, evalCfg)
}

// pollResults carries parsed outputs from completed generation jobs so that
// the caller can display them after both jobs finish.
type pollResults struct {
	EvaluatorResult *eval_api.EvaluatorResult
}

// pollAndFinalizeJobs polls pending dataset and evaluator generation jobs in
// parallel, saves artifacts when an azd project exists, and updates state and
// evalCfg. Jobs whose status is already terminal are skipped (safe for resume).
// extraEvals are prepended to the generated evaluator list on completion;
// pass nil for fresh inits without --evaluator flags.
func pollAndFinalizeJobs(
	ctx context.Context,
	resolved *evalResolvedContext,
	evalCfg *evalConfig,
	state *opt_eval.EvalState,
	extraEvals opt_eval.EvaluatorList,
) (*pollResults, error) {
	results := &pollResults{}
	// Each goroutine writes to distinct fields of evalCfg and state, so no
	// mutex is needed for those. Only the error variables are shared across
	// both goroutines and guarded by wg.Wait() (written before Wait, read after).
	var (
		datasetPollErr error
		evalPollErr    error
		wg             sync.WaitGroup
	)

	hasDataset := state.DatasetGenOpID != ""
	hasEval := state.EvalGenOpID != ""
	needPollDataset := hasDataset && !eval_api.ParseJobStatus(state.DatasetGenStatus).IsTerminal()
	needPollEval := hasEval && !eval_api.ParseJobStatus(state.EvalGenStatus).IsTerminal()

	// Build progress display labels (only for jobs that need polling).
	var labels []string
	if needPollDataset {
		labels = append(labels, "Dataset generation")
	}
	if needPollEval {
		labels = append(labels, "Evaluator generation")
	}
	progress := newEvalProgress(labels...)
	progress.Start()

	if hasDataset {
		wg.Go(func() {
			var completed *eval_api.GenerationJob
			if needPollDataset {
				var err error
				completed, err = pollEvalOperationWithSpinner(
					ctx, "Dataset generation", state.DatasetGenOpID,
					resolved.evalClient.GetDataGenerationJob, DataGenerationAPIVersion,
					progress,
				)
				if err != nil {
					datasetPollErr = fmt.Errorf("dataset generation job %s: %w", state.DatasetGenOpID, err)
					return
				}
			} else {
				// Job was already terminal at submission — fetch it directly.
				var err error
				completed, err = resolved.evalClient.GetDataGenerationJob(
					ctx, state.DatasetGenOpID, DataGenerationAPIVersion,
				)
				if err != nil {
					datasetPollErr = err
					return
				}
				if eval_api.ParseJobStatus(completed.NormalizedStatus()).IsFailed() {
					errMsg := fmt.Sprintf("dataset generation job %s failed", state.DatasetGenOpID)
					if completed.Error != nil && completed.Error.Message != "" {
						errMsg += ": " + completed.Error.Message
					}
					datasetPollErr = fmt.Errorf("%s", errMsg)
					return
				}
			}

			state.DatasetGenStatus = completed.NormalizedStatus()
			dsRef := datasetFromJob(completed)
			if dsRef == nil {
				return
			}
			evalCfg.DatasetReference = dsRef

			if resolved.hasProject {
				localURI, err := eval_api.DownloadDatasetArtifact(
					ctx, resolved.datasetClient, resolved.agentProject, dsRef, ProjectEndpointAPIVersion,
				)
				if err != nil {
					log.Printf("warning: downloading dataset artifact for %q: %v", dsRef.Name, err)
				}
				if localURI != "" {
					dsRef.LocalURI = localURI
				}
			}
		})
	}

	if hasEval {
		wg.Go(func() {
			var completed *eval_api.GenerationJob
			if needPollEval {
				var err error
				completed, err = pollEvalOperationWithSpinner(
					ctx, "Evaluator generation", state.EvalGenOpID,
					resolved.evalClient.GetEvaluatorGenerationJob, ProjectEndpointAPIVersion,
					progress,
				)
				if err != nil {
					evalPollErr = fmt.Errorf("evaluator generation job %s: %w", state.EvalGenOpID, err)
					return
				}
			} else {
				// Job was already terminal at submission — fetch it directly.
				var err error
				completed, err = resolved.evalClient.GetEvaluatorGenerationJob(
					ctx, state.EvalGenOpID, ProjectEndpointAPIVersion,
				)
				if err != nil {
					evalPollErr = err
					return
				}
				if eval_api.ParseJobStatus(completed.NormalizedStatus()).IsFailed() {
					errMsg := fmt.Sprintf("evaluator generation job %s failed", state.EvalGenOpID)
					if completed.Error != nil && completed.Error.Message != "" {
						errMsg += ": " + completed.Error.Message
					}
					evalPollErr = fmt.Errorf("%s", errMsg)
					return
				}
			}

			// Evaluator goroutine owns: state.EvalGenStatus, evalCfg.Evaluators.
			evalName, evalVersion := evaluatorFromJob(completed)
			state.EvalGenStatus = completed.NormalizedStatus()
			evalRef := opt_eval.EvaluatorRef{
				Name:     evalName,
				Version:  evalVersion,
				LocalURI: eval_api.EvaluatorLocalURI(evalName),
			}
			evalCfg.Evaluators = append(extraEvals, evalRef)

			results.EvaluatorResult = eval_api.ParseEvaluatorResult(completed.Result)

			if resolved.hasProject {
				if err := eval_api.SaveEvaluatorResult(resolved.agentProject, evalName, completed.Result); err != nil {
					log.Printf("warning: saving evaluator result for %q: %v", evalName, err)
				}
			}
		})
	}

	wg.Wait()
	progress.Stop()

	// If either job timed out, return a timeout error so the caller can
	// persist the YAML and operation IDs for later resume.
	dsTimeout := isPollerTimeout(datasetPollErr)
	evalTimeout := isPollerTimeout(evalPollErr)
	if dsTimeout || evalTimeout {
		return results, &initTimeoutError{
			datasetOpID:       state.DatasetGenOpID,
			evaluatorOpID:     state.EvalGenOpID,
			datasetTimedOut:   dsTimeout,
			evaluatorTimedOut: evalTimeout,
		}
	}

	if datasetPollErr != nil && evalPollErr != nil {
		return results, fmt.Errorf("%w\n%w", datasetPollErr, evalPollErr)
	}
	if datasetPollErr != nil {
		return results, datasetPollErr
	}
	return results, evalPollErr
}

// isPollerTimeout returns true when the error is a *eval_api.PollerTimeoutError.
func isPollerTimeout(err error) bool {
	_, ok := errors.AsType[*eval_api.PollerTimeoutError](err)
	return ok
}

// initTimeoutError is returned by pollAndFinalizeJobs when one or both
// generation jobs exceed the polling timeout. The caller should persist state
// and YAML so the user can resume later.
type initTimeoutError struct {
	datasetOpID       string
	evaluatorOpID     string
	datasetTimedOut   bool
	evaluatorTimedOut bool
}

func (e *initTimeoutError) Error() string {
	return "generation jobs did not complete within the polling timeout"
}

func writePendingEvalGenerate(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *opt_eval.EvalState,
) error {
	if err := opt_eval.SaveEvalState(ctx, resolved.azdClient, resolved.envName, state); err != nil {
		return err
	}
	if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
		return err
	}
	fmt.Println(color.YellowString("Eval generate submitted (async)"))
	if state.DatasetGenOpID != "" {
		fmt.Printf("   dataset generation: %s (%s)\n", state.DatasetGenOpID, state.DatasetGenStatus)
	}
	if state.EvalGenOpID != "" {
		fmt.Printf("   evaluator generation: %s (%s)\n", state.EvalGenOpID, state.EvalGenStatus)
	}
	fmt.Printf("\n   Config written to: %s\n", configPath)
	fmt.Println("\n   When ready, run:")
	fmt.Println("     azd ai agent eval run")
	return nil
}

// writeTimedOutEvalGenerate persists state and YAML when generation jobs exceed
// the polling timeout, allowing the user to resume later.
func writeTimedOutEvalGenerate(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *opt_eval.EvalState,
) error {
	state.InitStatus = opt_eval.InitStatusPending
	if err := opt_eval.SaveEvalState(ctx, resolved.azdClient, resolved.envName, state); err != nil {
		return err
	}
	if err := eval_api.WriteEvalConfig(configPath, evalCfg); err != nil {
		return err
	}
	fmt.Println(color.YellowString("\nGeneration jobs timed out but are still running on the server."))
	if state.DatasetGenOpID != "" {
		fmt.Printf("   dataset generation:   %s\n", state.DatasetGenOpID)
	}
	if state.EvalGenOpID != "" {
		fmt.Printf("   evaluator generation: %s\n", state.EvalGenOpID)
	}
	fmt.Printf("\n   Config written to: %s\n", configPath)
	fmt.Printf("   State saved to:    azd environment %q\n", resolved.envName)
	fmt.Println("\n   To resume polling, run:")
	fmt.Println("     azd ai agent eval run")
	fmt.Println("\n   To start fresh and clear timed-out state, run:")
	fmt.Println("     azd ai agent eval generate --reset-defaults")
	return nil
}

// tryLoadExistingEvalConfig attempts to load an eval config from the given path.
// Returns (config, true) if the file exists and parses successfully, or (nil, false) otherwise.
func tryLoadExistingEvalConfig(configPath string) (*evalConfig, bool) {
	cfg, err := eval_api.LoadEvalConfig(configPath)
	if err != nil {
		return nil, false
	}
	return cfg, true
}
