// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// What the reader can do with the generation plan.
const (
	generateProceed = iota
	generateChange
	generateCancel
)

// generateChoices is what the wizard settles about scope.
type generateChoices struct {
	dataset   bool
	evaluator bool
}

// askGenerateArtifacts settles what to generate.
//
// Only asked when neither narrowing flag was given: a caller who passed
// --dataset has already answered, and asking again would be reading their own
// flag back to them.
func (a *generateAction) askGenerateArtifacts() (generateChoices, error) {
	if a.flags.wantDataset || a.flags.wantEvaluator {
		return generateChoices{
			dataset:   a.flags.wantDataset,
			evaluator: a.flags.wantEvaluator,
		}, nil
	}
	both := generateChoices{dataset: true, evaluator: true}
	if noPrompt(a.cmd) {
		return both, nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return both, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	offered := []generateChoices{
		{dataset: true, evaluator: true},
		{dataset: true},
		{evaluator: true},
	}
	choices := make([]*azdext.SelectChoice, 0, len(offered))
	for _, o := range offered {
		label := messages.GenerateScopeChoice(o.dataset, o.evaluator)
		choices = append(choices, &azdext.SelectChoice{Label: label, Value: label})
	}

	resp, err := azdClient.Prompt().Select(commandContext(a.cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       messages.SelectGenerateScopePrompt(),
			Choices:       choices,
			SelectedIndex: preselect(0),
		},
	})
	if err != nil {
		return both, messages.SelectingGenerateScope(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// -- which here is "both", the same as the default, so an unanswered prompt
	// costs nothing extra.
	if resp == nil || resp.Value == nil {
		return both, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(offered) {
		return both, nil
	}
	return offered[index], nil
}

// promptSourceRetryConsent asks whether to submit the fallback generation job.
//
// The service rejects agent-seeded generation while accepting the same request
// carrying only the prompt, and retrying was automatic: a second job was billed
// against sources the caller never asked for, on a command they had confirmed
// once for one job. Under --no-prompt there is nobody to ask, so it does not
// retry -- the refusal names --from prompt, which asks for the surviving source
// directly.
func promptSourceRetryConsent(cmd *cobra.Command) retryConsent {
	return func(agent, jobID string, why error) (bool, error) {
		if noPrompt(cmd) || isJSON(cmd) {
			return false, nil
		}

		fmt.Fprint(cmd.ErrOrStderr(), messages.AgentSeedFailedHeading(agent, jobID, why))

		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			return false, messages.ConnectingToAzd(err)
		}
		defer azdClient.Close()

		stop := false
		resp, err := azdClient.Prompt().Confirm(commandContext(cmd), &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      messages.ConfirmPromptSourceRetryPrompt(),
				DefaultValue: &stop,
			},
		})
		if err != nil {
			return false, messages.ConfirmingPromptSourceRetry(err)
		}
		return resp.GetValue(), nil
	}
}

// generationSummary is what the confirmation reports.
type generationSummary struct {
	plans       []generationPlan
	model       string
	instructed  string
	projectName string
	configPath  string
	noWait      bool
}

// generateContext is what generate settled by reading, before it asked
// anything.
type generateContext struct {
	agent        string
	model        string
	projectName  string
	configPath   string
	configExists bool
	evals        int
}

// writeGenerateContext prints what was detected, before the first question.
//
// generate used to open on a prompt, which made the reader supply answers
// without seeing which agent, model and file the command had already picked --
// and picking the wrong project is the one mistake here that costs money.
func writeGenerateContext(out io.Writer, c generateContext) {
	fmt.Fprint(out, messages.LocalContextHeading())
	fmt.Fprint(out, messages.LocalContextLine("Agent", c.agent))
	fmt.Fprint(out, messages.LocalContextLine("Generation model", c.model))
	fmt.Fprint(out, messages.LocalContextLine("Project", c.projectName))
	fmt.Fprint(out, messages.LocalContextLine("Config file",
		messages.ConfigFileState(filepath.ToSlash(c.configPath), c.configExists, c.evals)))
}

// evalConfigState says whether there is already a configuration at path, and
// how many evals it holds. Best effort: this feeds a display line, and a
// configuration that will not parse is reported by whatever goes on to need it.
func evalConfigState(path string) (exists bool, evals int) {
	configPath, err := project.ResolveEvalConfigPath(path)
	if err != nil {
		return false, 0
	}
	if _, err := os.Stat(configPath); err != nil {
		return false, 0
	}
	// Nil without an error is how an absent configuration arrives, which the
	// stat above has already ruled out -- but the count is read through the
	// pointer, so the case that cannot happen is still not dereferenced.
	cfg, err := project.OpenEvalConfig(configPath)
	if err != nil || cfg == nil {
		return true, 0
	}
	return true, len(cfg.Evals)
}

// projectNameOf is the project segment of a Foundry endpoint.
//
// Read from the URL rather than asked for: the endpoint is what every call in
// this command goes to, so the name in it is the project the reader is about
// to spend money in, whether it came from a flag, the environment, or azd.
func projectNameOf(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || segments[len(segments)-2] != "projects" {
		return ""
	}
	return segments[len(segments)-1]
}

// confirmGeneration shows what is about to be billed and asks whether to.
//
// Generation calls a model and registers artifacts in a shared project, and
// none of that was confirmed: the first thing a reader saw was a job id for
// work already submitted. Nothing here has spent anything yet, which is what
// makes Cancel mean cancel.
func confirmGeneration(cmd *cobra.Command, out io.Writer, s generationSummary) (int, error) {
	if noPrompt(cmd) || isJSON(cmd) {
		// The flags are the confirmation. A plan block in front of every CI log
		// answers a question nobody is there to answer, and under -o json it is
		// not even valid output.
		return generateProceed, nil
	}

	writeGenerationPlan(out, s)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return generateCancel, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.ConfirmGenerationPrompt(),
			Choices: []*azdext.SelectChoice{
				{Label: messages.GenerateProceedChoice(), Value: "generate"},
				{Label: messages.GenerateChangeChoice(), Value: "change"},
				{Label: messages.GenerateCancelChoice(), Value: "cancel"},
			},
			SelectedIndex: preselect(generateProceed),
		},
	})
	if err != nil {
		return generateCancel, messages.ConfirmingGeneration(err)
	}
	// An unanswered prompt is not consent to bill anything. Value is optional on
	// the wire and an unset one arrives as 0 from GetValue, which is exactly the
	// answer that submits the jobs.
	if resp == nil || resp.Value == nil {
		return generateCancel, nil
	}
	switch int(resp.GetValue()) {
	case generateProceed:
		return generateProceed, nil
	case generateChange:
		return generateChange, nil
	default:
		return generateCancel, nil
	}
}

// writeGenerationPlan prints the block the confirmation is asked about.
func writeGenerationPlan(out io.Writer, s generationSummary) {
	fmt.Fprint(out, messages.GenerationPlanHeading())

	agent := ""
	for _, p := range s.plans {
		if p.Agent != "" {
			agent = p.Agent
			break
		}
	}
	if agent != "" {
		fmt.Fprint(out, messages.GenerationPlanLine("Agent", agent))
	}
	fmt.Fprint(out, messages.GenerationPlanLine("Model", s.model))
	// Printed either way. Silence when nothing was detected read as though the
	// question had not come up, and a generation seeded by nothing but the
	// agent's name is the one a reader most needs warning about.
	fmt.Fprint(out, messages.GenerationPlanLine(
		"Instructions", messages.InstructionsPlanValue(s.instructed)))

	for _, p := range s.plans {
		switch p.Kind {
		case generateKindDataset:
			fmt.Fprint(out, messages.GenerationPlanLine("Dataset", p.Name))
			fmt.Fprint(out, messages.GenerationPlanDetail(
				messages.DatasetPlanDetail(p.SampleSize, p.EvaluationLevel, p.From)))
		default:
			fmt.Fprint(out, messages.GenerationPlanLine("Evaluator", p.Name))
			fmt.Fprint(out, messages.GenerationPlanDetail(
				messages.EvaluatorPlanDetail(p.TraceDays)))
		}
	}

	if s.projectName != "" {
		fmt.Fprint(out, messages.GenerationPlanLine("Project", s.projectName))
	}
	fmt.Fprint(out, messages.GenerationPlanLine("Local",
		messages.GenerationPlanLocal(len(s.plans), filepath.ToSlash(s.configPath), s.noWait)))
	if s.noWait {
		fmt.Fprint(out, messages.GenerationPlanDetail(messages.GenerationPlanNoWait()))
	}
}
