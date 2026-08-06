// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaidataset/internal/pkg/gen_api"

	"github.com/spf13/cobra"
)

// generatePollBudget replaces the inherited 2s x 300 (10 minute) client budget.
// The generation job is not gateway-capped; the old limit simply gave up while
// the service was still working, forcing a second command.
var generatePollBudget = gen_api.PollerOptions{
	Interval:    5 * time.Second,
	MaxAttempts: 720, // one hour
}

// generationPlan is everything one generation job needs, after the flags have
// been reconciled.
type generationPlan struct {
	Name        string
	Agent       string
	Instruction string
	Model       string
	OutputDir   string
	SampleSize  int
	// From is what --from named: which of the service's sources to send. Empty
	// sends whatever the plan has to offer.
	From []string
	// TraceDays seeds generation from that many days of recent traces.
	TraceDays int
}

func (p generationPlan) traceOptions() *gen_api.TraceOptions {
	if p.TraceDays <= 0 {
		return nil
	}
	return &gen_api.TraceOptions{Days: p.TraceDays}
}

func newDatasetGenerateCommand() *cobra.Command {
	var (
		name            string
		target          string
		instruction     string
		instructionFile string
		model           string
		outputDir       string
		endpoint        string
		from            []string
		maxSamples      int
		traceDays       int
		noWait          bool
		force           bool
	)

	cmd := &cobra.Command{
		Use:   "generate <name>",
		Short: "Generate a dataset and download it.",
		Long: "Generate a dataset and download it.\n\n" +
			"--from selects one or more of the sources the service accepts, and " +
			"is repeatable. Generating from the agent's own definition is a " +
			"preference rather than a fallback: it covers cases no user has hit " +
			"yet, and it can supply reference answers, which a transcript cannot.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name = args[0]

			for _, src := range from {
				if err := validateGenerateSource(src); err != nil {
					return err
				}
			}
			if err := validateSampleSize(maxSamples); err != nil {
				return err
			}
			if instruction == "" && instructionFile != "" {
				body, err := os.ReadFile(instructionFile)
				if err != nil {
					return fmt.Errorf("reading --agent-instruction-file %q: %w", instructionFile, err)
				}
				instruction = strings.TrimSpace(string(body))
			}
			if model == "" {
				return errors.New("--generation-model is required")
			}

			plan := generationPlan{
				Name:        name,
				Agent:       target,
				Instruction: instruction,
				Model:       model,
				OutputDir:   outputDir,
				SampleSize:  maxSamples,
				From:        from,
				TraceDays:   traceDays,
			}
			if plan.OutputDir == "" {
				plan.OutputDir = defaultOutputDir
			}
			if plan.SampleSize == 0 {
				plan.SampleSize = defaultSampleSize
			}
			if err := refuseExistingArtifact(artifactPath(plan.OutputDir, plan.Name), force); err != nil {
				return err
			}

			ctx := cmd.Context()
			dc, err := newDatasetContext(ctx, endpoint)
			if err != nil {
				return err
			}
			defer dc.Close()

			if len(plan.From) == 0 {
				plan.From = defaultGenerationSource(dc.getEnvValue(ctx, appInsightsEnvKey))
			}
			plan.Instruction, err = dc.resolveGenerationInstruction(
				ctx, plan.Instruction, plan.Agent, cmd.OutOrStdout(), isJSON(cmd))
			if err != nil {
				return err
			}

			return dc.generateDataset(ctx, plan, cmd.OutOrStdout(), noWait, isJSON(cmd))
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Agent whose context seeds generation.")
	cmd.Flags().StringVar(&instruction, "agent-instruction", "",
		"What the agent does and what to test.")
	cmd.Flags().StringVar(&instructionFile, "agent-instruction-file", "",
		"Read the agent instruction from this file. Mutually exclusive with --agent-instruction.")
	cmd.MarkFlagsMutuallyExclusive("agent-instruction", "agent-instruction-file")
	cmd.Flags().StringVar(&model, "generation-model", "",
		"Model deployment that generates the dataset.")
	cmd.Flags().StringVar(&outputDir, "output-dir", "",
		fmt.Sprintf("Directory the generated dataset is written to. Defaults to %s.", defaultOutputDir))
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		fmt.Sprintf("Rows to synthesize (%d-%d). Defaults to %d.",
			minSampleSize, maxSampleSize, defaultSampleSize))
	cmd.Flags().StringSliceVar(&from, "from", nil,
		fmt.Sprintf("Where rows come from: %s. Repeatable, and the service accepts "+
			"more than one. Defaults to %s when the project has Application Insights "+
			"connected, otherwise %s.",
			strings.Join(generateSources, ", "), generateFromTraces, generateFromAgent))
	cmd.Flags().IntVar(&traceDays, "trace-days", 0,
		"Narrow a traces source to this many days of recent activity.")
	cmd.Flags().BoolVar(&noWait, "no-wait", false,
		"Submit the job and return its id without polling.")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite a dataset file that already exists.")
	cmd.Flags().StringVar(&endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// refuseExistingArtifact stops a generation that would overwrite a checked-in
// file, because the job is billed and the diff is what the author reviews.
func refuseExistingArtifact(path string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf(
			"%s already exists; pass --force to overwrite it, or --output-dir to write elsewhere",
			filepath.ToSlash(path))
	}
	return nil
}

// resolveGenerationInstruction decides what generation is seeded from: what the
// caller passed, then the agent's published instructions.
//
// The service accepts an agent source that is meant to pull those instructions,
// but it fails for every agent, so they are read here instead.
func (dc *datasetContext) resolveGenerationInstruction(
	ctx context.Context,
	explicit, agentName string,
	out io.Writer,
	quiet bool,
) (string, error) {
	if explicit != "" || agentName == "" {
		return explicit, nil
	}

	agent, err := dc.genClient.GetAgent(ctx, agentName, ProjectEndpointAPIVersion)
	if err != nil {
		// Generation can still proceed from the agent source alone, so a
		// failure to read the agent is reported without stopping.
		if !quiet {
			fmt.Fprintf(out, "  warning: could not read agent %q for generation context: %v\n",
				agentName, err)
		}
		return "", nil
	}
	instructions := agent.Instructions()
	if instructions != "" && !quiet {
		fmt.Fprintf(out, "  Seeding generation from the instructions of agent %q.\n", agentName)
	}
	return instructions, nil
}

// generateDataset submits the generation job and downloads what it produced.
func (dc *datasetContext) generateDataset(
	ctx context.Context,
	plan generationPlan,
	out io.Writer,
	noWait, quiet bool,
) error {
	if !quiet {
		fmt.Fprintf(out, "Generating dataset %s (%d samples)...\n", plan.Name, plan.SampleSize)
	}

	sources, unbuildable := gen_api.BuildGenerationSources(
		plan.From, plan.Agent, "", plan.Instruction, plan.traceOptions())
	if err := refuseUnbuildableSources(unbuildable); err != nil {
		return err
	}
	req := gen_api.NewDataGenerationJobRequest(plan.Name, plan.Model, plan.SampleSize, sources)

	job, err := dc.genClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
	if err != nil {
		return fmt.Errorf("submitting the data generation job: %w", err)
	}
	if noWait {
		fmt.Fprintf(out, "  submitted job %s\n", job.ID)
		fmt.Fprintf(out, "\nReattach with: azd ai dataset job show %s\n", job.ID)
		return nil
	}

	completed, err := dc.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
		dc.genClient.GetDataGenerationJob)
	if err != nil && isAgentSeededGenerationFailure(err) {
		// Agent-seeded generation fails server-side for every agent, while the
		// same request carrying only the prompt succeeds. Failing the whole
		// command would block the documented flow on a defect the user cannot
		// do anything about, so retry without the agent and say so.
		promptOnly := gen_api.WithoutAgentSource(sources)
		if gen_api.HasPromptSource(promptOnly) {
			fmt.Fprintf(out,
				"  warning: generating from agent %q failed in the service; "+
					"retrying from the instruction alone.\n", plan.Agent)

			req = gen_api.NewDataGenerationJobRequest(
				plan.Name, plan.Model, plan.SampleSize, promptOnly)
			job, err = dc.genClient.CreateDataGenerationJob(ctx, req, DataGenerationAPIVersion)
			if err != nil {
				return fmt.Errorf("submitting the data generation job: %w", err)
			}
			completed, err = dc.pollGeneration(ctx, job.ID, DataGenerationAPIVersion,
				dc.genClient.GetDataGenerationJob)
		}
	}
	if err != nil {
		return fmt.Errorf("data generation: %w", explainDataGenerationFailure(err, plan.Agent))
	}

	name, version := completed.ResolvedNameVersion()
	if name == "" {
		return fmt.Errorf("the data generation job returned no dataset reference")
	}

	// Confirm the version exists before reading it, so a missing dataset is
	// reported as such rather than as a download failure.
	if _, err := dc.datasetClient.GetDataset(ctx, name, version, ProjectEndpointAPIVersion); err != nil {
		return fmt.Errorf("reading the generated dataset %q: %w", name, err)
	}
	content, err := dc.datasetClient.DownloadDatasetContent(
		ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return fmt.Errorf("downloading the generated dataset %q: %w", name, err)
	}

	path := artifactPath(plan.OutputDir, plan.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing %q: %w", path, err)
	}

	if quiet {
		return emitJSON(out, map[string]any{
			"dataset": name, "version": version, "path": filepath.ToSlash(path),
		})
	}
	fmt.Fprintf(out, "  wrote %s\n", filepath.ToSlash(path))

	// The datasets: catalog belongs to `azure.ai.eval`, which owns eval.yaml,
	// so registering the file there is a separate step rather than a silent
	// cross-extension write.
	fmt.Fprintf(out, "\nNext: azd ai eval dataset create %s --from-file %s\n",
		plan.Name, filepath.ToSlash(path))
	return nil
}

// pollGeneration waits for a generation job using the raised budget.
func (dc *datasetContext) pollGeneration(
	ctx context.Context,
	operationID, apiVersion string,
	get gen_api.GetJobFunc,
) (*gen_api.GenerationJob, error) {
	poller := gen_api.NewPoller(operationID, apiVersion, get)
	poller.Options = generatePollBudget
	return poller.Poll(ctx)
}

// refuseUnbuildableSources reports a --from the plan could not honour.
//
// Submitting anyway would run a billed job seeded from less than was asked for
// and return a plausible-looking artifact, which is the worst outcome: the
// caller has no way to tell it apart from one built the way they intended.
func refuseUnbuildableSources(kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	reasons := map[string]string{
		generateFromPrompt: "--from prompt needs --agent-instruction or --agent-instruction-file",
		generateFromAgent:  "--from agent needs a target agent; pass --target",
		generateFromFile: "--from file is not a generation source; " +
			"register the file with `azd ai dataset create` instead",
	}
	messages := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if reason, ok := reasons[k]; ok {
			messages = append(messages, reason)
			continue
		}
		messages = append(messages, fmt.Sprintf("--from %s cannot be built from this plan", k))
	}
	return errors.New(strings.Join(messages, "; "))
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
// error, which says only that something went wrong and to try again — sending
// users into a retry loop against a deterministic failure.
func explainDataGenerationFailure(err error, agentName string) error {
	if err == nil || agentName == "" || !isAgentSeededGenerationFailure(err) {
		return err
	}
	return fmt.Errorf(
		"%w\n\nSeeding generation from agent %q fails server-side for every agent. "+
			"Pass --agent-instruction to generate from the instruction alone.",
		err, agentName)
}
