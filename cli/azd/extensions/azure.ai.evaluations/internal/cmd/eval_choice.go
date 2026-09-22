// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errEvalSelectionCancelled is a reader closing the eval picker.
//
// Distinct from choosing nothing: an empty name falls through to the ambiguity
// error, which tells someone who declined to choose that they failed to name
// something.
var errEvalSelectionCancelled = errors.New("eval selection cancelled")

// cancelled reports whether a prompt failed because the reader closed it.
//
// Only meaningful once the command's own context has been ruled out: an
// interrupted command cancels the prompt too, and the two look alike here.
func cancelled(err error) bool {
	return status.Code(err) == codes.Canceled || errors.Is(err, context.Canceled)
}

// selectionOutcome reads a failed picker: an interrupt, a close, or a prompt
// that could not run.
//
// The command's context being done means the reader interrupted the whole
// command, which is not the same as closing this one prompt. Treating it as a
// close reported a Ctrl-C as an answer and exited 0, so a script that was
// interrupted mid-run read as one that succeeded.
func selectionOutcome(cmd *cobra.Command, named string, err error) (string, error) {
	if ctxErr := commandContext(cmd).Err(); ctxErr != nil {
		return "", ctxErr
	}
	if cancelled(err) {
		return "", errEvalSelectionCancelled
	}
	// Any other reason the prompt could not run leaves the name as it was, so
	// the caller's own error still describes what is missing.
	return named, nil
}

// isEvalSelectionCancelled reports a closed eval picker.
//
// A predicate rather than a bare errors.Is at each site, so every command that
// has to recognize the answer recognizes it the same way.
func isEvalSelectionCancelled(err error) bool {
	return errors.Is(err, errEvalSelectionCancelled)
}

// reportCancelledSelection tells the reader the picker was closed.
//
// One place, because the words are the whole point: closing the picker on
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
// Returning the name empty leaves the existing error to the caller, which is
// what happens whenever the prompt cannot run. A prompt the reader closed is
// the one case that does not: that is an answer, and it is returned as one.
func chooseEval(cmd *cobra.Command, cfg *project.EvalConfig, named string) (string, error) {
	if named != "" || cfg == nil || len(cfg.Evals) < 2 || noPrompt(cmd) {
		return named, nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return named, nil
	}
	defer azdClient.Close()

	names := cfg.EvalNames()
	choices := make([]*azdext.SelectChoice, 0, len(names))
	for i := range names {
		choices = append(choices, &azdext.SelectChoice{Label: names[i], Value: names[i]})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         messages.SelectEvalPrompt(),
			Choices:         choices,
			EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil {
		return selectionOutcome(cmd, named, err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from
	// GetValue -- indistinguishable from the first choice. Reading it as a
	// selection would start a billed run against an eval nobody picked.
	if resp == nil || resp.Value == nil {
		return named, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(names) {
		return named, nil
	}
	return names[index], nil
}
