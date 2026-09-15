// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// Cobra defaults to ArbitraryArgs, which accepts positional input and then
// ignores it, so a mistyped `dataset list my-set` would report on everything
// while looking like it had been narrowed. Every leaf this extension owns has
// to say what it takes; walk already skips the ones azd contributes.
func TestEveryLeafCommandDeclaresWhatItTakes(t *testing.T) {
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		if len(cmd.Commands()) > 0 {
			return
		}
		assert.NotNil(t, cmd.Args,
			"%s accepts and silently discards positional arguments", path)
	})
}
