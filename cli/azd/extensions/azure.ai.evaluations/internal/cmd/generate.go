// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// generatePollBudget replaces the inherited 2s x 300 (10 minute) client budget.
// The generation job is not gateway-capped; the old limit simply gave up while
// the service was still working, forcing a second command.
var generatePollBudget = eval_api.PollerOptions{
	Interval:    5 * time.Second,
	MaxAttempts: 720, // one hour
}

func newGenerateCommand() *cobra.Command {
	var (
		configPath      string
		deployPath      string
		target          string
		instruction     string
		instructionFile string
		datasetFlag     string
		evaluators      []string
		maxSamples      int
		traceDays       int
		evalModel       string
		noWait          bool
		endpointFlg     string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a rubric and dataset, download them, and reference them from the deployment spec.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			instruction, err := resolveInstruction(instruction, instructionFile)
			if err != nil {
				return err
			}

			cfg, err := resolveGenerateConfig(
				configPath, target, evalModel, datasetFlag, maxSamples, traceDays,
			)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			// Written to stdout because azd does not surface an extension's
			// stderr, and guarded so `-o json` stays parseable.
			if !isJSON(cmd) {
				warnIgnoredTraceFields(cfg, out)
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			baseDir := filepath.Dir(deployPath)
			var datasetRefs, evaluatorRefs []project.ArtifactRef

			// Supplied evaluators are honored: their generation is skipped.
			if len(evaluators) > 0 {
				fmt.Fprintf(out, "Using the supplied evaluators; skipping rubric generation.\n")
			} else if cfg.Generate.Rubric != nil {
				ref, err := ec.generateRubric(ctx, cfg, instruction, baseDir, out, noWait)
				if err != nil {
					return err
				}
				if ref != nil {
					evaluatorRefs = append(evaluatorRefs, *ref)
				}
			}

			// --dataset means use this one, whether it names a local file or a
			// dataset already registered on the project. Either way there is
			// nothing to generate, which is how --evaluator behaves too.
			if datasetFlag != "" {
				fmt.Fprintf(out, "Using the supplied dataset; skipping data generation.\n")
			} else if cfg.Generate.Dataset != nil {
				ref, err := ec.generateDataset(ctx, cfg, instruction, baseDir, out, noWait)
				if err != nil {
					return err
				}
				if ref != nil {
					datasetRefs = append(datasetRefs, *ref)
				}
			}

			if len(datasetRefs) == 0 && len(evaluatorRefs) == 0 {
				// With --no-wait the jobs were submitted and nothing was
				// downloaded, which is success, not an empty result.
				if noWait {
					fmt.Fprintln(out,
						"\nJobs submitted. Re-run without --no-wait to download the artifacts "+
							"and reference them from the deployment spec.")
					return nil
				}
				fmt.Fprintln(out, "Nothing was generated.")
				return nil
			}

			if err := project.MergeArtifactRefs(deployPath, datasetRefs, evaluatorRefs); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nUpdated %s\n", deployPath)
			fmt.Fprintln(out, "Review the generated artifacts, then run: azd up && azd ai eval run")
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", project.DefaultGenerateConfig,
		"Path to the generation spec. Optional; flags alone are sufficient.")
	cmd.Flags().StringVar(&deployPath, "deploy-config", project.DefaultDeployConfig,
		"Deployment spec to write source references into.")
	cmd.Flags().StringVar(&target, "target", "", "Agent whose context seeds generation.")
	cmd.Flags().StringVar(&instruction, "gen-instruction", "",
		"What the agent does and what to test.")
	cmd.Flags().StringVar(&instructionFile, "gen-instruction-file", "",
		"Read the generation instruction from this file. Mutually exclusive with --gen-instruction.")
	cmd.MarkFlagsMutuallyExclusive("gen-instruction", "gen-instruction-file")
	cmd.Flags().StringVar(&datasetFlag, "dataset", "",
		"Use this dataset instead of generating one.")
	cmd.Flags().StringArrayVar(&evaluators, "evaluator", nil,
		"Use these evaluators instead of generating a rubric; repeatable.")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		fmt.Sprintf("Rows to synthesize (%d-%d).", project.MinSampleSize, project.MaxSampleSize))
	cmd.Flags().IntVar(&traceDays, "trace-days", 0,
		"Days of traces to seed rubric generation. 0 disables.")
	cmd.Flags().StringVar(&evalModel, "eval-model", "", "Model deployment used for generation.")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Submit the jobs and return without polling.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// warnIgnoredTraceFields reports trace settings that are accepted but have no
// effect yet.
//
// The generation API takes a day window and nothing else, so `source` and
// `sample` are parsed and dropped. Silently discarding them is worse than not
// accepting them: the author believes they narrowed the trace selection when
// nothing changed.
func warnIgnoredTraceFields(cfg *project.GenerateConfig, out io.Writer) {
	traces := cfg.Agent.Context.Traces
	if traces == nil {
		return
	}

	var ignored []string
	if traces.Source != "" {
		ignored = append(ignored, "source")
	}
	if traces.Sample > 0 {
		ignored = append(ignored, "sample")
	}
	if len(ignored) == 0 {
		return
	}

	fields := make([]string, 0, len(ignored))
	for _, name := range ignored {
		fields = append(fields, "agent.context.traces."+name)
	}

	verb := "has"
	if len(fields) > 1 {
		verb = "have"
	}
	fmt.Fprintf(out,
		"warning: %s %s no effect yet; trace seeding uses only `window`. "+
			"Trace selection lands with the trace scenarios.\n",
		strings.Join(fields, " and "), verb)
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
		return "", fmt.Errorf("reading --gen-instruction-file %q: %w", path, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("--gen-instruction-file %q is empty", path)
	}
	return text, nil
}

// resolveGenerateConfig loads the spec when present, then layers flags on top.
// A missing file is not an error: flags alone are sufficient.
func resolveGenerateConfig(
	path, target, evalModel, datasetFlag string,
	maxSamples, traceDays int,
) (*project.GenerateConfig, error) {
	cfg := &project.GenerateConfig{}

	if _, err := os.Stat(path); err == nil {
		loaded, err := project.LoadGenerateConfig(path)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}

	if target != "" {
		cfg.Agent.Name = target
	}
	if cfg.Agent.Name == "" {
		return nil, requireFlag("target")
	}

	if cfg.Generate.Rubric == nil {
		cfg.Generate.Rubric = &project.RubricSpec{
			Name:     cfg.Agent.Name + "-quality",
			LocalDir: "./" + project.DefaultEvaluatorsDir,
		}
	}
	if cfg.Generate.Dataset == nil && datasetFlag == "" {
		cfg.Generate.Dataset = &project.DatasetSpec{
			Name:       cfg.Agent.Name + "-golden",
			Strategy:   project.StrategySynthetic,
			SampleSize: project.DefaultSampleSize,
			LocalDir:   "./" + project.DefaultDatasetsDir,
		}
	}

	if evalModel != "" {
		cfg.Generate.Rubric.Model = evalModel
	}
	if maxSamples > 0 && cfg.Generate.Dataset != nil {
		cfg.Generate.Dataset.SampleSize = maxSamples
	}
	if cfg.Generate.Dataset != nil && cfg.Generate.Dataset.SampleSize == 0 {
		cfg.Generate.Dataset.SampleSize = project.DefaultSampleSize
	}
	if traceDays > 0 {
		if cfg.Agent.Context.Traces == nil {
			cfg.Agent.Context.Traces = &project.TraceSpec{}
		}
		cfg.Agent.Context.Traces.Window = fmt.Sprintf("%dd", traceDays)
	}

	return cfg, nil
}

// generateRubric submits the evaluator generation job and saves the rubric.
func (ec *evalContext) generateRubric(
	ctx context.Context,
	cfg *project.GenerateConfig,
	instruction, baseDir string,
	out io.Writer,
	noWait bool,
) (*project.ArtifactRef, error) {
	spec := cfg.Generate.Rubric
	fmt.Fprintf(out, "Generating rubric %s...\n", spec.Name)

	sources := eval_api.BuildGenerationSources(
		"agent", cfg.Agent.Name, "", instruction, traceOptions(cfg),
	)
	req := eval_api.NewEvaluatorGenerationJobRequest(spec.Name, spec.Model, sources)

	job, err := ec.evalClient.CreateEvaluatorGenerationJob(ctx, req, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("submitting the rubric generation job: %w", err)
	}
	if noWait {
		fmt.Fprintf(out, "  submitted job %s\n", job.ID)
		return nil, nil
	}

	completed, err := ec.pollGeneration(ctx, job.ID, ProjectEndpointAPIVersion,
		ec.evalClient.GetEvaluatorGenerationJob)
	if err != nil {
		return nil, fmt.Errorf("rubric generation: %w", err)
	}

	path := project.ArtifactPath(baseDir, spec.LocalDir, spec.Name, ".json")
	if err := writeRubric(path, completed.Result); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "  wrote %s\n", path)

	return &project.ArtifactRef{Name: spec.Name, Source: relativeSource(baseDir, path)}, nil
}

// generateDataset submits the data generation job and downloads the result.
func (ec *evalContext) generateDataset(
	ctx context.Context,
	cfg *project.GenerateConfig,
	instruction, baseDir string,
	out io.Writer,
	noWait bool,
) (*project.ArtifactRef, error) {
	spec := cfg.Generate.Dataset
	fmt.Fprintf(out, "Generating dataset %s (%d samples)...\n", spec.Name, spec.SampleSize)

	sources := eval_api.BuildGenerationSources(
		"agent", cfg.Agent.Name, "", instruction, traceOptions(cfg),
	)
	model := ""
	if cfg.Generate.Rubric != nil {
		model = cfg.Generate.Rubric.Model
	}
	req := eval_api.NewDataGenerationJobRequest(spec.Name, model, spec.SampleSize, sources)

	job, err := ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("submitting the data generation job: %w", err)
	}
	if noWait {
		fmt.Fprintf(out, "  submitted job %s\n", job.ID)
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
			fmt.Fprintf(out,
				"  warning: generating from agent %q failed in the service; "+
					"retrying from the instruction alone.\n", cfg.Agent.Name)

			req = eval_api.NewDataGenerationJobRequest(
				spec.Name, model, spec.SampleSize, promptOnly)
			job, err = ec.evalClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
			if err != nil {
				return nil, fmt.Errorf("submitting the data generation job: %w", err)
			}
			completed, err = ec.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
				ec.evalClient.GetDataGenerationJob)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("data generation: %w", explainDataGenerationFailure(err, cfg.Agent.Name))
	}

	name, version := completed.ResolvedNameVersion()
	if name == "" {
		return nil, fmt.Errorf("the data generation job returned no dataset reference")
	}

	// Confirm the version exists before reading it, so a missing dataset is
	// reported as such rather than as a download failure.
	if _, err := ec.datasetClient.GetDataset(
		ctx, name, version, ProjectEndpointAPIVersion,
	); err != nil {
		return nil, fmt.Errorf("reading the generated dataset %q: %w", name, err)
	}
	content, err := ec.datasetClient.DownloadDatasetContent(ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("downloading the generated dataset %q: %w", name, err)
	}

	path := project.ArtifactPath(baseDir, spec.LocalDir, spec.Name, ".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, fmt.Errorf("writing %q: %w", path, err)
	}
	fmt.Fprintf(out, "  wrote %s\n", path)

	return &project.ArtifactRef{Name: spec.Name, Source: relativeSource(baseDir, path)}, nil
}

// isAgentSeededGenerationFailure recognises the service-side failure that hits
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
	return fmt.Errorf(
		"%w\n\n"+
			"This job seeded generation from agent %q. Agent-seeded data generation is "+
			"currently failing in the service for every agent, so retrying will not help.\n"+
			"Workarounds: supply your own dataset with --dataset, or run without --target "+
			"to generate from the instruction alone.",
		err, agentName)
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

// traceOptions converts the config's trace window into the generation client's
// day count. Traces seed rubric generation only; they are never a run's data
// source.
func traceOptions(cfg *project.GenerateConfig) *eval_api.TraceOptions {
	t := cfg.Agent.Context.Traces
	if t == nil {
		return nil
	}
	days := parseWindowDays(t.Window)
	if days <= 0 {
		return nil
	}
	return &eval_api.TraceOptions{Days: days}
}

// parseWindowDays reads a window such as "30d" or a bare day count.
func parseWindowDays(window string) int {
	w := strings.TrimSpace(strings.ToLower(window))
	if w == "" {
		return 0
	}
	w = strings.TrimSuffix(w, "d")
	days, err := strconv.Atoi(w)
	if err != nil {
		return 0
	}
	return days
}

// writeRubric persists only the rubric dimensions so the developer can edit
// weights and descriptions and publish a new version.
func writeRubric(path string, result json.RawMessage) error {
	if len(result) == 0 {
		return fmt.Errorf("the rubric generation job returned no result")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %q: %w", filepath.Dir(path), err)
	}

	var parsed eval_api.EvaluatorResult
	if err := json.Unmarshal(result, &parsed); err == nil && len(parsed.Definition.Dimensions) > 0 {
		body, err := json.MarshalIndent(parsed.Definition, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, body, 0o600)
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
