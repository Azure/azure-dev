// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb that takes a <name> refuses an invalid one locally.
//
// The sibling extension carries a second copy of these commands, and the guard
// reached its dataset verbs but not its evaluator ones. Walking the tree rather
// than listing the verbs is the point: a verb added later is covered without
// anyone remembering to add it here.
func TestEveryNamedAssetVerbRefusesAnInvalidName(t *testing.T) {
	const badName = "has space"

	checked := 0
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if cmd.RunE == nil || !strings.Contains(cmd.Use, "<name>") {
			return
		}

		checked++
		// The guard has to come first: it runs before the client is built, so
		// a mistyped name costs neither a round trip nor an azd connection.
		err := cmd.RunE(cmd, []string{badName})
		require.Errorf(t, err, "%s accepted %q", path, badName)
		assert.Containsf(t, err.Error(), "is invalid",
			"%s refused %q, but not by naming the name", path, badName)
	})

	assert.GreaterOrEqual(t, checked, 4,
		"create, update, show, delete and versions list all take a name")
}
