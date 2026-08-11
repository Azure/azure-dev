// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/spf13/cobra"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// defaultEvaluators is what `init` proposes: one built-in that judges whether
// the agent did what was asked, plus a rubric generated from the agent's own
// instructions, which is what makes the criteria specific to this agent.
//
// The rubric is not offered for a trace-backed eval, which has no target to
// read instructions from.
func defaultEvaluators(rubricName string, traceBacked bool) []string {
	refs := []string{evalcore.BuiltinPrefix + "task_adherence"}
	if !traceBacked {
		refs = append(refs, rubricName)
	}
	return refs
}

// evaluatorChoices are the references `init` can offer.
//
// `init` makes no service calls, so the service's full built-in catalogue is
// not knowable here; offering a hardcoded copy of it would drift. What is
// knowable is the pair init proposes and whatever this configuration already
// declares. Anything else is reachable with --evaluator.
func evaluatorChoices(cfg *project.EvalConfig, rubricName string, traceBacked bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}

	for _, ref := range defaultEvaluators(rubricName, traceBacked) {
		add(ref)
	}
	if cfg != nil {
		for _, decl := range cfg.Evaluators {
			add(decl.Name)
		}
	}
	return out
}

// resolveEvaluators settles what the eval grades on.
//
// Unlike the target and the judge model, there is no "the only one" here: an
// eval grades on a SET, and which criteria define quality for this agent is the
// substantive decision in the whole configuration. So this asks rather than
// detects, with the defaults preselected. Under --no-prompt the preselection
// stands, which is what keeps CI and the init -> generate flow working.
//
// The second return says whether the reader chose. Only a set decided FOR them
// is worth reporting back; echoing a selection they just made is noise.
func resolveEvaluators(
	cmd *cobra.Command,
	cfg *project.EvalConfig,
	rubricName string,
	traceBacked bool,
) ([]string, bool, error) {
	defaults := defaultEvaluators(rubricName, traceBacked)
	if noPrompt(cmd) {
		return defaults, false, nil
	}
	chosen, err := promptEvaluators(cmd, evaluatorChoices(cfg, rubricName, traceBacked), defaults)
	if err != nil {
		return nil, false, err
	}
	return chosen, true, nil
}

// promptEvaluators asks which references to grade with, defaults ticked.
func promptEvaluators(cmd *cobra.Command, choices, preselected []string) ([]string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	ticked := map[string]bool{}
	for _, p := range preselected {
		ticked[p] = true
	}
	opts := make([]*azdext.MultiSelectChoice, 0, len(choices))
	for i := range choices {
		opts = append(opts, &azdext.MultiSelectChoice{
			Label:    choices[i],
			Value:    choices[i],
			Selected: ticked[choices[i]],
		})
	}

	resp, err := azdClient.Prompt().MultiSelect(cmd.Context(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: messages.SelectEvaluatorsPrompt(),
			Choices: opts,
		},
	})
	if err != nil {
		return nil, messages.SelectingEvaluators(err)
	}

	chosen := make([]string, 0, len(resp.GetValues()))
	for _, v := range resp.GetValues() {
		chosen = append(chosen, v.GetValue())
	}
	if len(chosen) == 0 {
		// An eval that grades on nothing is rejected by the service, and the
		// refusal names none of this.
		return nil, messages.NoEvaluatorsChosen()
	}
	return chosen, nil
}
