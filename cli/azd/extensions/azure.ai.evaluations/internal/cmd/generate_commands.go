// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

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

	// Only consulted when --target was not given, so an ambiguous configuration
	// is not an error for a caller who already said which agent they meant.
	agent := f.target
	if agent == "" {
		if agent, err = declaredTarget(f.path); err != nil {
			return generationPlan{}, err
		}
	}

	plan := generationPlan{
		Name:        name,
		Agent:       agent,
		Model:       f.model,
		Instruction: instruction,
		BaseDir:     project.EvalDirOf(f.path),
		OutputDir:   firstNonEmpty(f.outputDir, "./"+defaultOutputDir),
	}
	if instruction != "" {
		plan.InstructionSource = messages.InstructionSourceFlag(f.instructionFile)
	}
	// Neither is settled yet: the agent can still come from the project's own
	// services and the model from the azd environment, and both of those are
	// looked up in prepareGeneration. Refusing here made a bare `eval generate`
	// fail in a project that declares exactly one of each.
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

	// The eval config named no agent, so ask the project. One declared agent is
	// a detection; several is a question. Without this a bare `eval generate` in
	// a single-agent project went on to fail deriving artifact names, having
	// prompted for everything else first.
	if plan.Agent == "" {
		if plan.Agent, err = ec.detectAgentTarget(cmd); err != nil {
			ec.Close()
			return nil, plan, err
		}
	}

	plan.Instruction, plan.InstructionSource, err = ec.resolveGenerationInstruction(
		cmd, plan.Instruction, plan.InstructionSource, plan.Agent, cmd.OutOrStdout(), isJSON(cmd),
	)
	if err != nil {
		ec.Close()
		return nil, plan, err
	}

	if plan.Model == "" {
		plan.Model = ec.agentDeployment(ctx, plan.Agent, cmd.OutOrStdout(), isJSON(cmd))
	}
	if plan.Model == "" {
		// What `azd ai agent init` chose is recorded in the azd environment, and
		// binding to an existing Foundry project leaves it as the only record.
		// Reading it is the difference between a configured project generating
		// and being told to pass a flag it already knows the answer to.
		plan.Model = modelDeploymentFromAzdEnv(ctx)
	}
	if plan.Model == "" {
		ec.Close()
		return nil, plan, messages.GenerationModelRequired()
	}
	return ec, plan, nil
}

// detectAgentTarget settles the agent when nothing named one.
//
// The project's own services are the answer `init` already uses, so the two
// commands agree about what "the agent" means. One is a detection and needs no
// prompt; several is a question, and under --no-prompt it names the flag.
// Outside a project there is nothing to detect and generation carries on from
// the instruction alone, which is what a bare directory supports.
func (ec *evalContext) detectAgentTarget(cmd *cobra.Command) (string, error) {
	proj, err := ec.azdProject(cmd.Context())
	if err != nil || proj == nil {
		return "", nil
	}
	agents := agentServices(proj)
	switch len(agents) {
	case 0:
		return "", nil
	case 1:
		return agents[0], nil
	}
	if noPrompt(cmd) {
		return "", messages.AmbiguousAgentTarget(agents)
	}
	return promptAgentTarget(cmd, agents)
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
//
// A file may declare evals for several agents. Taking the first one silently
// sent a billed job the wrong agent's instructions and model, so more than one
// distinct target is refused and --target has to say which.
func declaredTarget(evalDir string) (string, error) {
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil || cfg == nil {
		return "", nil
	}

	seen := map[string]bool{}
	targets := make([]string, 0, len(cfg.Evals))
	for _, eval := range cfg.Evals {
		if eval.Target == nil || eval.Target.Name == "" || seen[eval.Target.Name] {
			continue
		}
		// A model target names a deployment, not an agent. Inferring one handed
		// GetAgent a deployment name and reported the miss as a missing
		// --generation-model, and a file carrying one of each read as ambiguous.
		// An absent type is an agent, which is what the schema defaults to.
		if eval.Target.Type == project.TargetTypeModel {
			continue
		}
		seen[eval.Target.Name] = true
		targets = append(targets, eval.Target.Name)
	}

	switch len(targets) {
	case 0:
		return "", nil
	case 1:
		return targets[0], nil
	default:
		return "", messages.AmbiguousAgentTarget(targets)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
