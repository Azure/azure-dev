// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/fatih/color"
)

func resolveEvalName(flags *evalInitFlags) string {
	if flags.name != "" {
		return flags.name
	}
	return defaultEvalName
}

// randomSuffix returns a short random hex string (4 bytes = 8 chars).
func randomSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}

func newEvalConfig(flags *evalInitFlags, resolved *evalResolvedContext) *evalConfig {
	return &evalConfig{
		Config: opteval.Config{
			Name: resolveEvalName(flags),
			Agent: evalAgentRef{
				Name:    resolved.agentName,
				Kind:    resolved.agentKind,
				Version: resolved.version,
			},
		},
		Options: &opteval.Options{
			EvalModel: flags.evalModel,
		},
		GenerationInstruction: flags.genInstruction,
		MaxSamples:            flags.maxSamples,
		TraceDays:             flags.traceDays,
	}
}

func submitDatasetGeneration(
	ctx context.Context,
	resolved *evalResolvedContext,
	flags *evalInitFlags,
) (*eval_api.GenerationJob, error) {
	// Traces are only supported for evaluator generation, not dataset generation.
	sources := eval_api.BuildGenerationSources(
		string(resolved.agentKind), resolved.agentName, resolved.version, flags.genInstruction, nil,
	)
	request := eval_api.NewDataGenerationJobRequest(
		resolveEvalName(flags), flags.evalModel, flags.maxSamples, sources,
	)
	if body, err := json.MarshalIndent(request, "", "  "); err == nil {
		log.Printf("[debug] submitDatasetGeneration request:\n%s", body)
	}
	return resolved.evalClient.CreateDataGenerationJob(ctx, request, DataGenerationAPIVersion)
}

func submitEvaluatorGeneration(
	ctx context.Context,
	resolved *evalResolvedContext,
	flags *evalInitFlags,
) (*eval_api.GenerationJob, error) {
	var traces *eval_api.TraceOptions
	if flags.traceDays > 0 {
		traces = &eval_api.TraceOptions{Days: flags.traceDays}
	}
	sources := eval_api.BuildGenerationSources(
		string(resolved.agentKind), resolved.agentName, resolved.version, flags.genInstruction, traces,
	)
	request := eval_api.NewEvaluatorGenerationJobRequest(
		resolveEvalName(flags), flags.evalModel, sources,
	)
	if body, err := json.MarshalIndent(request, "", "  "); err == nil {
		log.Printf("[debug] submitEvaluatorGeneration request:\n%s", body)
	}
	return resolved.evalClient.CreateEvaluatorGenerationJob(ctx, request, DefaultAgentAPIVersion)
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
	return &evalDatasetRef{
		Name:    job.ResolvedDatasetName(),
		Version: job.ResolvedDatasetVersion(),
	}
}

func evaluatorFromJob(job *eval_api.GenerationJob) string {
	return job.ResolvedEvaluatorName()
}

func evaluatorsFromFlags(values []string) []string {
	return values
}

func buildOpenAIEvalRequest(evalCfg *evalConfig) *eval_api.CreateOpenAIEvalRequest {
	return evalCfg.ToAgentTargetAdaptableEvalGroupRequest()
}

func resumeEvalInit(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *evalState,
) error {
	if err := pollAndFinalizeJobs(ctx, resolved, evalCfg, state, nil); err != nil {
		if _, ok := errors.AsType[*initTimeoutError](err); ok {
			return writeTimedOutEvalInit(ctx, resolved, configPath, evalCfg, state)
		}
		return err
	}
	state.InitStatus = "completed"
	clearEvalState(ctx, resolved.azdClient, resolved.envName)
	if resolved.hasProject {
		writeEvalReviewArtifacts(resolved.projectRoot, evalCfg)
	}
	return writeEvalConfig(configPath, evalCfg)
}

// pollAndFinalizeJobs polls pending dataset and evaluator generation jobs in
// parallel, saves artifacts when an azd project exists, and updates state and
// evalCfg. Jobs whose status is already terminal are skipped (safe for resume).
// builtinEvals are prepended to the generated evaluator name on completion;
// pass nil for fresh inits.
func pollAndFinalizeJobs(
	ctx context.Context,
	resolved *evalResolvedContext,
	evalCfg *evalConfig,
	state *evalState,
	builtinEvals []string,
) error {
	var (
		mu             sync.Mutex
		datasetPollErr error
		evalPollErr    error
		wg             sync.WaitGroup
	)

	pollDataset := state.DatasetGenOpID != "" &&
		!eval_api.ParseJobStatus(state.DatasetGenStatus).IsTerminal()
	pollEval := state.EvalGenOpID != "" &&
		!eval_api.ParseJobStatus(state.EvalGenStatus).IsTerminal()

	// When both jobs run in parallel, disable individual spinners to avoid
	// overlapping terminal output. Print status lines upfront instead.
	parallel := pollDataset && pollEval
	if parallel {
		fmt.Println("  Waiting for generation jobs...")
		fmt.Printf("    - Dataset generation:   %s\n", state.DatasetGenOpID)
		fmt.Printf("    - Evaluator generation: %s\n", state.EvalGenOpID)
	}

	if pollDataset {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completed, err := pollEvalOperationWithSpinner(
				ctx, "Dataset generation", state.DatasetGenOpID,
				resolved.evalClient.GetDataGenerationJob, DataGenerationAPIVersion,
				!parallel,
			)
			if err != nil {
				mu.Lock()
				datasetPollErr = err
				mu.Unlock()
				return
			}
			mu.Lock()
			state.DatasetGenStatus = completed.NormalizedStatus()
			mu.Unlock()
			dsRef := datasetFromJob(completed)
			if resolved.hasProject {
				saveDatasetGenerationResult(
					resolved.projectRoot, completed.ResolvedDatasetName(), completed.Result,
				)
				if err := downloadDatasetArtifact(
					ctx, resolved.datasetClient, resolved.projectRoot, dsRef, DefaultAgentAPIVersion,
				); err != nil {
					mu.Lock()
					datasetPollErr = err
					mu.Unlock()
					return
				}
				mu.Lock()
				evalCfg.DatasetFile = datasetArtifactPath(resolved.projectRoot, dsRef)
				mu.Unlock()
			}
		}()
	}

	if pollEval {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completed, err := pollEvalOperationWithSpinner(
				ctx, "Evaluator generation", state.EvalGenOpID,
				resolved.evalClient.GetEvaluatorGenerationJob, DefaultAgentAPIVersion,
				!parallel,
			)
			if err != nil {
				mu.Lock()
				evalPollErr = err
				mu.Unlock()
				return
			}
			evalName := evaluatorFromJob(completed)
			mu.Lock()
			state.EvalGenStatus = completed.NormalizedStatus()
			evalCfg.Evaluators = append(builtinEvals, evalName)
			mu.Unlock()
			if resolved.hasProject {
				saveEvaluatorResult(resolved.projectRoot, evalName, completed.Result)
			}
		}()
	}

	wg.Wait()

	// If either job timed out, return a timeout error so the caller can
	// persist the YAML and operation IDs for later resume.
	dsTimeout := isPollerTimeout(datasetPollErr)
	evalTimeout := isPollerTimeout(evalPollErr)
	if dsTimeout || evalTimeout {
		return &initTimeoutError{
			datasetOpID:       state.DatasetGenOpID,
			evaluatorOpID:     state.EvalGenOpID,
			datasetTimedOut:   dsTimeout,
			evaluatorTimedOut: evalTimeout,
		}
	}

	if datasetPollErr != nil {
		return datasetPollErr
	}
	return evalPollErr
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

func writePendingEvalInit(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *evalState,
) error {
	if err := saveEvalState(ctx, resolved.azdClient, resolved.envName, state); err != nil {
		return err
	}
	if err := writeEvalConfig(configPath, evalCfg); err != nil {
		return err
	}
	fmt.Println(color.YellowString("Eval init submitted (async)"))
	if state.DatasetGenOpID != "" {
		fmt.Printf("   dataset generation: %s (%s)\n", state.DatasetGenOpID, state.DatasetGenStatus)
	}
	if state.EvalGenOpID != "" {
		fmt.Printf("   evaluator generation: %s (%s)\n", state.EvalGenOpID, state.EvalGenStatus)
	}
	fmt.Printf("\n   Config written to: %s\n", configPath)
	fmt.Printf("   State saved to:    azd environment %q\n", resolved.envName)
	fmt.Println("\n   When ready, run:")
	fmt.Println("     azd ai agent eval run")
	return nil
}

// writeTimedOutEvalInit persists state and YAML when generation jobs exceed
// the polling timeout, allowing the user to resume later.
func writeTimedOutEvalInit(
	ctx context.Context,
	resolved *evalResolvedContext,
	configPath string,
	evalCfg *evalConfig,
	state *evalState,
) error {
	state.InitStatus = "pending"
	if err := saveEvalState(ctx, resolved.azdClient, resolved.envName, state); err != nil {
		return err
	}
	if err := writeEvalConfig(configPath, evalCfg); err != nil {
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
	fmt.Println("     azd ai agent eval init")
	fmt.Println("\n   To start fresh and clear timed-out state, run:")
	fmt.Println("     azd ai agent eval init --reset-defaults")
	return nil
}

// tryLoadExistingEvalConfig attempts to load an eval config from the given path.
// Returns (config, true) if the file exists and parses successfully, or (nil, false) otherwise.
func tryLoadExistingEvalConfig(configPath string) (*evalConfig, bool) {
	cfg, err := readEvalConfig(configPath)
	if err != nil {
		return nil, false
	}
	return cfg, true
}
