// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
func confirmDelete(cmd *cobra.Command, ec *evalContext, subject string, force bool) error {
	if force {
		return nil
	}
	if noPrompt(cmd) {
		return messages.DeleteNeedsForce(subject)
	}

	defaultNo := false
	resp, err := ec.azdClient.Prompt().Confirm(cmd.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      messages.ConfirmDelete(subject),
			DefaultValue: &defaultNo,
		},
	})
	if err != nil {
		return exterrors.FromPrompt(err, "confirming the delete")
	}
	if resp.GetValue() {
		return nil
	}
	return messages.DeleteCancelled(subject)
}

// registerForceFlag adds the flag every delete verb needs to run unattended.
func registerForceFlag(cmd *cobra.Command, force *bool) {
	cmd.Flags().BoolVar(force, "force", false, "Delete without asking for confirmation.")
}
