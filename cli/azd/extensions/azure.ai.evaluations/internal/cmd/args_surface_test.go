// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// Cobra defaults to ArbitraryArgs, which accepts positional input and ignores
// it. On `run start` that billed a run against the default eval when the caller
// named a different one, so every leaf this extension owns has to say what it
// takes. The SDK owns `version`, `metadata` and `listen`.
func TestEveryLeafCommandDeclaresWhatItTakes(t *testing.T) {
	sdkOwned := map[string]bool{"version": true, "metadata": true, "listen": true}

	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		full := strings.TrimSpace(path + " " + c.Name())
		if len(c.Commands()) == 0 && !sdkOwned[c.Name()] {
			assert.NotNil(t, c.Args,
				"%s accepts and silently discards positional arguments", full)
		}
		for _, sub := range c.Commands() {
			walk(sub, full)
		}
	}
	walk(NewRootCommand(), "")
}

// The two that were wrong, pinned by behaviour rather than by the validator's
// identity: both are reached without arguments and both take none.
func TestRunStartAndInitRefuseAPositionalEvalName(t *testing.T) {
	for _, tc := range []struct{ path []string }{
		{[]string{"run", "start"}},
		{[]string{"init"}},
	} {
		name := strings.Join(tc.path, " ")
		t.Run(name, func(t *testing.T) {
			cmd, _, err := NewRootCommand().Find(tc.path)
			assert.NoError(t, err)
			assert.NoError(t, cmd.Args(cmd, []string{}),
				"%s has to remain callable with no arguments", name)
			assert.Error(t, cmd.Args(cmd, []string{"some-eval"}),
				"%s must refuse a name it would otherwise ignore", name)
		})
	}
}
