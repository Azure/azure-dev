// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// confirmDelete asks before removing something published, and refuses rather
// than assume when nobody can answer.
//
// One helper for every delete verb in this extension. `azd ai dataset delete`
// and `azd ai eval dataset delete` are the same operation to a user, and they
// used to differ: one asked, the other removed the data immediately. The
// asymmetry was invisible until someone typed the wrong name.
//
// A prompt written into a JSON document, or into a script running with
// --no-prompt, is a hang rather than a question, which is why those require the
// flag instead of defaulting either way.
//
// Returns whether to go ahead, separately from whether anything went wrong.
// Answering no is the command working: the reader was asked, they said no, and
// nothing was deleted. Reporting that as an error exits 1, which makes a
// deliberate answer look like a failure to every script that checks.
func confirmDelete(
	cmd *cobra.Command,
	ec *evalContext,
	subject string,
	force bool,
) (bool, error) {
	if force {
		return true, nil
	}
	if noPrompt(cmd) {
		return false, messages.DeleteNeedsForce(subject)
	}

	defaultNo := false
	resp, err := ec.azdClient.Prompt().Confirm(cmd.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      messages.ConfirmDelete(subject),
			DefaultValue: &defaultNo,
		},
	})
	if err != nil {
		return false, exterrors.FromPrompt(err, "confirming the delete")
	}
	return resp.GetValue(), nil
}

// deleteDeclined reports the answer and is the whole of what a declined delete
// does, so every caller ends it the same way.
func deleteDeclined(cmd *cobra.Command, subject string) error {
	fmt.Fprint(cmd.OutOrStdout(), messages.DeleteCancelled(subject))
	return nil
}

// registerForceFlag adds the flag every delete verb needs to run unattended.
func registerForceFlag(cmd *cobra.Command, force *bool) {
	cmd.Flags().BoolVar(force, "force", false, "Delete without asking for confirmation.")
}
