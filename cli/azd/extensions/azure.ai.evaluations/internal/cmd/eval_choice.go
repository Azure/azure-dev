// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// errEvalSelectionCancelled is an explicit Cancel answer, not a prompt error.
var errEvalSelectionCancelled = errors.New("eval selection cancelled")

// selectionOutcome preserves prompt errors even when the command context is live:
// the host handles Ctrl+C by cancelling only its own prompt context.
func selectionOutcome(cmd *cobra.Command, err error) (string, error) {
	if ctxErr := commandContext(cmd).Err(); ctxErr != nil {
		return "", ctxErr
	}
	return "", messages.SelectingEval(err)
}

// isEvalSelectionCancelled reports an explicit Cancel answer.
//
// A predicate rather than a bare errors.Is at each site, so every command that
// has to recognize the answer recognizes it the same way.
func isEvalSelectionCancelled(err error) bool {
	return errors.Is(err, errEvalSelectionCancelled)
}

// reportCancelledSelection tells the reader they chose Cancel.
//
// One place, because the words are the whole point: choosing Cancel on
// `eval create` and on `run show` is the same answer, and saying it differently
// at each door reads as different outcomes.
//
// Through humanOut, because these commands exit 0 afterwards: under -o json a
// direct write leaves successful output that does not parse as JSON.
func reportCancelledSelection(cmd *cobra.Command) {
	fmt.Fprint(humanOut(cmd, cmd.OutOrStdout()), messages.EvalSelectionCancelled())
}

// chooseEvalIn is chooseEval for the run commands, which hold a directory
// rather than a loaded configuration. A configuration that will not open is
// left to the command that opens it properly, so the error stays the same one.
func chooseEvalIn(cmd *cobra.Command, evalDir, named string) (string, error) {
	if named != "" || noPrompt(cmd) {
		return named, nil
	}
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil {
		return named, nil
	}
	return chooseEval(cmd, cfg, named)
}

// chooseEval settles which eval a command means when the caller named none.
//
// Refusing is right under --no-prompt, where there is nobody to ask. Standing
// at a terminal it is not: the command holds the whole candidate list, and the
// documented scenarios declare a second eval, so every bare `run start` after
// that would fail permanently.
//
// An unanswered response leaves the existing ambiguity error to the caller.
// Only the explicit Cancel choice is a successful no-op; a prompt error,
// including an interrupt, must reach the caller.
func chooseEval(cmd *cobra.Command, cfg *project.EvalConfig, named string) (string, error) {
	if named != "" || cfg == nil || len(cfg.Evals) < 2 || noPrompt(cmd) {
		return named, nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	names := cfg.EvalNames()
	choices := make([]*azdext.SelectChoice, 0, len(names)+1)
	for i := range names {
		choices = append(choices, &azdext.SelectChoice{Label: names[i], Value: names[i]})
	}
	choices = append(choices, &azdext.SelectChoice{Label: messages.CancelEvalChoice(), Value: "cancel"})

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         messages.SelectEvalPrompt(),
			Choices:         choices,
			EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil || commandContext(cmd).Err() != nil {
		return selectionOutcome(cmd, err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from
	// GetValue -- indistinguishable from the first choice. Reading it as a
	// selection would start a billed run against an eval nobody picked.
	if resp == nil || resp.Value == nil {
		return named, nil
	}
	index := int(resp.GetValue())
	if index == len(names) {
		return "", errEvalSelectionCancelled
	}
	if index < 0 || index >= len(names) {
		return named, nil
	}
	return names[index], nil
}
