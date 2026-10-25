// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// builtinEvaluators is the offline picker shortlist, not a catalog allowlist.
// Other built-ins remain available through explicit --evaluator references.
var builtinEvaluators = []string{
	evalcore.BuiltinPrefix + "task_completion",
	evalcore.BuiltinPrefix + "customer_satisfaction",
	evalcore.BuiltinPrefix + "coherence",
	evalcore.BuiltinPrefix + "groundedness",
}

// builtinCatalogueTimeout bounds the one listing init asks for.
//
// init is the command run before anything is set up, often on a laptop with no
// project reachable, so waiting on a transport that is not going to answer
// costs more than the check is worth.
const builtinCatalogueTimeout = 5 * time.Second

// catalogueContext puts the bound on the one listing init asks for.
//
// Named rather than inlined so the bound is something a test can observe. A
// test that stands up an unreachable endpoint proves only that the endpoint is
// unreachable: it returns before the bound either way, and deleting the bound
// leaves it green. The deadline this derives is the behavior itself.
//
// Derived from the caller's context, not from Background: the listing is best
// effort, but a reader interrupting the command is not, and a detached context
// would go on waiting after they had already answered.
func catalogueContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, builtinCatalogueTimeout)
}

// hasBuiltinRef reports whether any reference names a built-in.
//
// The catalogue can only answer for those, so a run with none -- or with only
// custom references -- has nothing to ask about, and opening a connection to
// learn that costs the offline path a wait it need not take.
func hasBuiltinRef(refs []string) bool {
	for _, ref := range refs {
		if strings.HasPrefix(ref, evalcore.BuiltinPrefix) {
			return true
		}
	}
	return false
}

// readBuiltinEvaluatorCatalogue asks the project which built-in evaluators it
// offers.
//
// Best effort, and deliberately so. init's value is that it works with nothing
// configured, so no azd, no endpoint, no network, an unauthorized project or a
// listing that fails all answer the same way: nothing is known, and every
// reference is left as written. Only a catalogue that was actually read is
// allowed to refuse a name.
func readBuiltinEvaluatorCatalogue(ctx context.Context) []string {
	ctx, cancel := catalogueContext(ctx)
	defer cancel()

	ec, err := newEvalContext(ctx, "")
	if err != nil {
		return nil
	}
	defer ec.Close()

	list, err := ec.evalClient.ListEvaluators(
		ctx, eval_api.EvaluatorTypeBuiltin, ProjectEndpointAPIVersion)
	if err != nil || list == nil {
		return nil
	}

	names := make([]string, 0, len(list.Value))
	for i := range list.Value {
		if name := strings.TrimSpace(list.Value[i].Name); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// refuseUnknownBuiltins refuses a builtin.<name> the catalogue does not offer.
//
// A nil catalogue means the listing was not read; a non-nil empty list means
// the project returned no built-in names.
//
// Names are matched with and without the prefix. The service returns them
// prefixed today, and a reference that matches either spelling is a reference
// to something real -- which is the question being asked.
func refuseUnknownBuiltins(refs []string, known []string) error {
	if known == nil {
		return nil
	}

	offered := make(map[string]struct{}, len(known)*2)
	for _, name := range known {
		offered[name] = struct{}{}
		offered[evalcore.BuiltinPrefix+strings.TrimPrefix(name, evalcore.BuiltinPrefix)] = struct{}{}
	}

	for _, ref := range refs {
		if !strings.HasPrefix(ref, evalcore.BuiltinPrefix) {
			// A bare name is a rubric this configuration declares or will
			// generate, and the catalogue says nothing about it.
			continue
		}
		if _, ok := offered[ref]; ok {
			continue
		}
		if _, ok := offered[strings.TrimPrefix(ref, evalcore.BuiltinPrefix)]; ok {
			continue
		}
		return messages.EvaluatorBuiltinUnknown(ref, known)
	}
	return nil
}

// defaultEvaluators proposes the existing task-completion default.
func defaultEvaluators() []string {
	return []string{builtinEvaluators[0]}
}

// evaluatorChoices are the references `init` can offer.
//
// Initial choices use the offline shortlist and this configuration's declarations,
// not an enumeration of the service's full built-in catalogue.
// resolveEvaluators filters these choices against the catalogue when it was read.
// Other built-ins remain reachable with --evaluator, which is checked against the
// catalogue when the project can be reached.
func evaluatorChoices(cfg *project.EvalConfig, level string) []string {
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
			if initEvaluatorSupportsLevel(&decl, level) {
				add(decl.Name)
			}
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
	level string,
	knownBuiltins []string,
) ([]string, bool, error) {
	defaults := defaultEvaluators()
	if noPrompt(cmd) {
		if err := refuseUnknownBuiltins(defaults, knownBuiltins); err != nil {
			return nil, false, err
		}
		return defaults, false, nil
	}
	choices := evaluatorChoices(cfg, level)
	choices = slices.DeleteFunc(choices, func(ref string) bool {
		return refuseUnknownBuiltins([]string{ref}, knownBuiltins) != nil
	})
	chosen, err := promptEvaluators(cmd, choices, defaults)
	if err != nil {
		return nil, false, err
	}
	if err := refuseUnknownBuiltins(chosen, knownBuiltins); err != nil {
		return nil, false, err
	}
	if cfg != nil {
		if err := validateInitEvaluatorLevels(cfg, chosen, level); err != nil {
			return nil, false, err
		}
	}
	return chosen, true, nil
}

// initEvaluatorSupportsLevel applies the same compatibility contract as
// reconciliation: an empty list is unconstrained; a nonempty list must match.
func initEvaluatorSupportsLevel(decl *project.EvaluatorDecl, level string) bool {
	schema := eval_api.EvaluatorSummary{SupportedEvaluationLevels: decl.SupportedEvaluationLevels}
	return schema.SupportsLevel(level)
}

func validateInitEvaluatorLevels(cfg *project.EvalConfig, refs []string, level string) error {
	for _, ref := range refs {
		decl, ok := cfg.EvaluatorDeclaration(ref)
		if ok && !initEvaluatorSupportsLevel(decl, level) {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				fmt.Sprintf("--evaluator %s declares support for %s, not --evaluation-level %s",
					ref, strings.Join(decl.SupportedEvaluationLevels, ", "), level),
				"Choose an evaluator that supports the selected level, or change --evaluation-level.")
		}
	}
	return nil
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
