// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

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
		require.NoError(t, confirmDelete(cmd, nil, "golden", "3", true),
			"the caller already said yes on the command line")
	})

	t.Run("--no-prompt without --force is refused", func(t *testing.T) {
		cmd := deleteCommandWith(t, "--no-prompt")
		err := confirmDelete(cmd, nil, "golden", "3", false)

		require.Error(t, err, "nobody is there to answer, so it must not assume yes")
		assert.Contains(t, err.Error(), "--force", "the message has to say how to proceed")
		assert.Contains(t, err.Error(), "golden")
		assert.Contains(t, err.Error(), "3")
	})

	t.Run("json output is treated as unattended", func(t *testing.T) {
		cmd := deleteCommandWith(t, "--output", "json")
		err := confirmDelete(cmd, nil, "golden", "3", false)

		require.Error(t, err,
			"a question written into a JSON document is a hang, not a prompt")
		assert.Contains(t, err.Error(), "--force")
	})
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
