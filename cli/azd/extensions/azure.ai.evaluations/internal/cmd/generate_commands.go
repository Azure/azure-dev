// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// Generation is split per artifact because the service splits it: datasets and
// evaluators are separate long-running resources. One composite verb leaves
// partial failure undefined, cannot regenerate one artifact after the other has
// been hand-edited, and gives --no-wait nothing to reattach to.
//
// Neither command edits azure.yaml. Both add a catalog entry to azure.eval.yaml for
// what they produced, so the artifact is referenceable without a hand edit.

// generateFlags are the settings both generate commands share.
//
// There is no generation spec file. Every setting is a flag, because the
// artifact is checked in and a regeneration usually wants different settings
// anyway; what that costs is provenance, which is Open Question 8.
type generateFlags struct {
	path            string
	target          string
	instruction     string
	instructionFile string
	model           string
	outputDir       string
	noWait          bool
	force           bool
	endpoint        string
}

func addGenerateFlags(cmd *cobra.Command, f *generateFlags) {
	cmd.Flags().StringVar(&f.path, "path", "",
		"Directory holding the evaluation configuration. Defaults to the directory "+
			"`init` scaffolded, otherwise ./evals.")
	cmd.Flags().StringVar(&f.target, "target", "", "Agent whose context seeds generation.")
	cmd.Flags().StringVar(&f.instruction, "agent-instruction", "",
		"What the agent does and what to test.")
	cmd.Flags().StringVar(&f.instructionFile, "agent-instruction-file", "",
		"Read the agent instruction from this file. Mutually exclusive with --agent-instruction.")
	cmd.MarkFlagsMutuallyExclusive("agent-instruction", "agent-instruction-file")
	cmd.Flags().StringVar(&f.model, "generation-model", "",
		"Model deployment that generates the artifact.")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "",
		"Directory the generated artifact is written to.")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false,
		"Submit the job and return its id without polling.")
	// Waiting is already the default, so --wait only changes anything when it is
	// turned off. Parsed into a variable nobody reads, `--wait=false` -- a legal
	// spelling -- would be accepted and then do the opposite of what it says.
	var wait bool
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the job finishes.")
	cmd.MarkFlagsMutuallyExclusive("wait", "no-wait")
	cmd.PreRun = func(*cobra.Command, []string) {
		if !wait {
			f.noWait = true
		}
	}
	cmd.Flags().BoolVar(&f.force, "force", false,
		"Overwrite an artifact file that already exists.")
	cmd.Flags().StringVar(&f.endpoint, "project-endpoint", "", "Foundry project endpoint.")
}

// resolvePlan settles every input that does not need the network.
//
// Doing it before the client is built means a missing model or an out-of-range
// sample count is refused without an authentication round trip. The instruction
// file is read here rather than later so that an input the caller named and got
// wrong is reported ahead of one they simply left out.
func resolvePlan(f *generateFlags, name string, defaultOutputDir string) (generationPlan, error) {
	instruction, err := resolveInstruction(f.instruction, f.instructionFile)
	if err != nil {
		return generationPlan{}, err
	}

	plan := generationPlan{
		Name:        name,
		Agent:       firstNonEmpty(f.target, declaredTarget(f.path)),
		Model:       f.model,
		Instruction: instruction,
		BaseDir:     f.path,
		OutputDir:   firstNonEmpty(f.outputDir, "./"+defaultOutputDir),
	}
	if plan.Model == "" && plan.Agent == "" {
		return plan, messages.GenerationModelRequired()
	}
	return plan, nil
}

// prepareGeneration builds the client and settles the two inputs that need it:
// the agent's published instructions, and its deployment when the caller named
// no model of its own. Only the service can supply either.
func prepareGeneration(
	cmd *cobra.Command,
	f *generateFlags,
	plan generationPlan,
) (*evalContext, generationPlan, error) {
	ctx := cmd.Context()
	ec, err := newEvalContext(ctx, f.endpoint)
	if err != nil {
		return nil, plan, err
	}

	plan.Instruction, err = ec.resolveGenerationInstruction(
		ctx, plan.Instruction, plan.Agent, cmd.OutOrStdout(), isJSON(cmd),
	)
	if err != nil {
		ec.Close()
		return nil, plan, err
	}

	if plan.Model == "" {
		plan.Model = ec.agentDeployment(ctx, plan.Agent, cmd.OutOrStdout(), isJSON(cmd))
	}
	if plan.Model == "" {
		ec.Close()
		return nil, plan, messages.GenerationModelRequired()
	}
	return ec, plan, nil
}

// agentDeployment reads the deployment the target agent answers with.
//
// Best effort, but not silent: a misspelled --target and an agent with no
// published version both end in "pass --generation-model", which names neither.
// The warning is what tells those two apart.
func (ec *evalContext) agentDeployment(
	ctx context.Context,
	agentName string,
	out io.Writer,
	quiet bool,
) string {
	if agentName == "" {
		return ""
	}
	agent, err := ec.evalClient.GetAgent(ctx, agentName, ProjectEndpointAPIVersion)
	if err != nil {
		if !quiet {
			fmt.Fprint(out, messages.CouldNotReadAgentForModel(agentName, err))
		}
		return ""
	}
	return agent.Model()
}

// declaredTarget reads the agent from the evaluation configuration, which is
// where the target is already declared, so `generate` does not need it
// repeated. Best effort: generation runs from the instruction alone when there
// is no configuration to read, which is the case in a bare directory.
func declaredTarget(evalDir string) string {
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil || cfg == nil {
		return ""
	}
	for _, eval := range cfg.Evals {
		if eval.Target != nil && eval.Target.Name != "" {
			return eval.Target.Name
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// refuseExistingArtifact stops a generation that would overwrite a checked-in
// file, because the job is billed and the diff is what the author reviews.
func refuseExistingArtifact(path string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return messages.ArtifactExists(filepath.ToSlash(path))
	}
	return nil
}
