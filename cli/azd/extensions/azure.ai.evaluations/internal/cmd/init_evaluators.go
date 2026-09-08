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

// builtinEvaluators are the four `init` offers, and they judge at either
// evaluation level, so Turn and Conversation share one picker.
//
// A hardcoded list drifts from the service's full catalogue, which is why this
// is deliberately the offered set rather than a copy of it: init makes no
// service call, and anything outside these four is still reachable with
// --evaluator.
var builtinEvaluators = []string{
	evalcore.BuiltinPrefix + "task_completion",
	evalcore.BuiltinPrefix + "customer_satisfaction",
	evalcore.BuiltinPrefix + "coherence",
	evalcore.BuiltinPrefix + "groundedness",
}

// defaultEvaluators is what `init` proposes: one built-in that judges whether
// the agent did what was asked.
//
// It used to add a rubric generated from the agent's instructions, which meant
// init declared an evaluator file nothing had produced. The eval then referred
// to a rubric that did not exist until a separate generate ran, and `azd up`
// failed on it. Generation is its own command; init writes only what is there.
func defaultEvaluators() []string {
	return []string{builtinEvaluators[0]}
}

// evaluatorChoices are the references `init` can offer.
//
// `init` makes no service calls, so the service's full built-in catalogue is
// not knowable here; offering a hardcoded copy of it would drift. What is
// knowable is the pair init proposes and whatever this configuration already
// declares. Anything else is reachable with --evaluator.
func evaluatorChoices(cfg *project.EvalConfig) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}

	for _, ref := range builtinEvaluators {
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
) ([]string, bool, error) {
	defaults := defaultEvaluators()
	if noPrompt(cmd) {
		return defaults, false, nil
	}
	chosen, err := promptEvaluators(cmd, evaluatorChoices(cfg), defaults)
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

	resp, err := azdClient.Prompt().MultiSelect(commandContext(cmd), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message:         messages.SelectEvaluatorsPrompt(),
			Choices:         opts,
			EnableFiltering: filteringFor(len(opts)),
		},
	})
	if err != nil {
		return nil, messages.SelectingEvaluators(err)
	}

	chosen := make([]string, 0, len(resp.GetValues()))
	for _, v := range resp.GetValues() {
		// A blank choice would otherwise be written to the config as an
		// evaluator named "", and looked up as one two commands later.
		if v.GetValue() == "" {
			continue
		}
		chosen = append(chosen, v.GetValue())
	}
	if len(chosen) == 0 {
		// An eval that grades on nothing is rejected by the service, and the
		// refusal names none of this.
		return nil, messages.NoEvaluatorsChosen()
	}
	return chosen, nil
}
