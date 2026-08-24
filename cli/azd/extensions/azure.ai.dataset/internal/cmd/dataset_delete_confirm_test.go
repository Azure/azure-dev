// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaidataset/internal/messages"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deleting a version asks first, and refuses rather than assume when nobody
// can answer.
//
// The command removed the remote version the moment it was invoked, with no
// question and no way to say yes in advance. Every other Foundry delete asks by
// default and requires --force without a terminal; this one now does too.
func TestDeleteAsksBeforeRemovingAVersion(t *testing.T) {
	t.Run("--force skips the question", func(t *testing.T) {
		cmd := deleteCommandWith(t, "--no-prompt")
		goAhead, err := confirmDelete(cmd, nil, "golden", "3", true)

		require.NoError(t, err, "the caller already said yes on the command line")
		assert.True(t, goAhead, "--force is the answer, so the delete goes ahead")
	})

	t.Run("--no-prompt without --force is refused", func(t *testing.T) {
		cmd := deleteCommandWith(t, "--no-prompt")
		goAhead, err := confirmDelete(cmd, nil, "golden", "3", false)

		require.Error(t, err, "nobody is there to answer, so it must not assume yes")
		assert.False(t, goAhead)
		assert.Contains(t, err.Error(), "--force", "the message has to say how to proceed")
		assert.Contains(t, err.Error(), "golden")
		assert.Contains(t, err.Error(), "3")
	})

	t.Run("json output is treated as unattended", func(t *testing.T) {
		cmd := deleteCommandWith(t, "--output", "json")
		goAhead, err := confirmDelete(cmd, nil, "golden", "3", false)

		require.Error(t, err,
			"a question written into a JSON document is a hang, not a prompt")
		assert.False(t, goAhead)
		assert.Contains(t, err.Error(), "--force")
	})
}

// Answering no is the command working, not failing.
//
// It used to come back as an ordinary error, which azdext reports and exits 1
// on, so a reader who deliberately said no got the same exit code as a reader
// whose delete broke. Every script that checks the code reads that as a
// failure. The refusal above still errors, because there the command could not
// do what it was asked; declining is the command doing exactly what it was
// asked.
func TestDecliningIsNotAFailure(t *testing.T) {
	assert.IsType(t, "", messages.DeleteCancelled("golden", "3"),
		"a decline is something to print, not something to return as an error")
	assert.Contains(t, messages.DeleteCancelled("golden", "3"), "golden",
		"the line has to name what was left alone")
}

// The delete command exposes the flag the contract depends on.
func TestDeleteCommandOffersForce(t *testing.T) {
	cmd := newDatasetDeleteCommand()

	force := cmd.Flags().Lookup("force")
	require.NotNil(t, force, "without --force there is no way to delete unattended")
	assert.Equal(t, "false", force.DefValue, "asking is the default")

	assert.Contains(t, cmd.Long, "--force",
		"the help has to state the contract, since the flag changes what the command destroys")
}

// deleteCommandWith builds a delete command carrying the global flags the
// helper reads, parsed as azd would hand them over.
func deleteCommandWith(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := newDatasetDeleteCommand()
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	require.NoError(t, cmd.Flags().Parse(args))
	return cmd
}
