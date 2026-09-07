// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"azureaieval/internal/messages"

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

// generationSummary is what the confirmation reports.
type generationSummary struct {
	plans      []generationPlan
	model      string
	instructed bool
	configPath string
	noWait     bool
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
	if s.instructed {
		fmt.Fprint(out, messages.GenerationPlanLine("Instructions", "detected"))
	}

	for _, p := range s.plans {
		switch p.Kind {
		case generateKindDataset:
			fmt.Fprint(out, messages.GenerationPlanLine("Dataset", p.Name))
			fmt.Fprint(out, messages.GenerationPlanDetail(
				messages.DatasetPlanDetail(p.SampleSize, p.From)))
		default:
			fmt.Fprint(out, messages.GenerationPlanLine("Evaluator", p.Name))
			fmt.Fprint(out, messages.GenerationPlanDetail(
				messages.EvaluatorPlanDetail(p.TraceDays)))
		}
	}

	fmt.Fprint(out, messages.GenerationPlanLine("Local",
		messages.GenerationPlanLocal(len(s.plans), filepath.ToSlash(s.configPath), s.noWait)))
	if s.noWait {
		fmt.Fprint(out, messages.GenerationPlanDetail(messages.GenerationPlanNoWait()))
	}
}
