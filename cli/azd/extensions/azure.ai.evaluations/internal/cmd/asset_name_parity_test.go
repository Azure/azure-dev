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
// The guard was written for the dataset verbs and never reached the evaluator
// ones, so `azd ai eval dataset show "my set"` explained itself and
// `azd ai eval evaluator show "my rubric"` returned the service's 400 wrapped
// in four levels of JSON. Walking the tree rather than listing the verbs is
// the point: a verb added later is covered without anyone remembering to.
//
// The name has to be one both rules refuse. Creating applies the service's
// character set, so a typo is caught before the round trip; looking something
// up does not, because `generate` publishes names that set would reject and
// what it published has to be readable back. A separator is refused either way.
func TestEveryNamedAssetVerbRefusesAnInvalidName(t *testing.T) {
	const badName = "bad/name"

	var walk func(cmd *cobra.Command, path string)
	checked := 0

	walk = func(cmd *cobra.Command, path string) {
		for _, sub := range cmd.Commands() {
			walk(sub, strings.TrimSpace(path+" "+sub.Name()))
		}
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
	}

	walk(newDatasetCommand(), "dataset")
	walk(newEvaluatorCommand(), "evaluator")

	assert.GreaterOrEqual(t, checked, 8,
		"both command groups take a name on create, update, show, delete and versions list")
}
