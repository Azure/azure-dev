// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

// pflag reads the first back-quoted word of a usage string as the name of the
// flag's value, so prose that quotes a command renames the placeholder.
//
// `--path` was documented as "the directory `init` scaffolded" on three
// commands and `azd ai eval init --help` rendered it as `--path init`, which
// reads as a required literal. Nothing here means to override a placeholder, so
// the rule is that no usage string carries a backtick at all -- a narrower rule
// would have to decide which overrides are intentional, and none are.
func TestNoFlagUsageRenamesItsValuePlaceholder(t *testing.T) {
	var checked int
	walk(t, NewRootCommand(), nil, func(path string, cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			checked++
			assert.NotContainsf(t, f.Usage, "`",
				"%s --%s: a back-quoted word becomes the value placeholder", path, f.Name)
		})
	})
	assert.NotZero(t, checked, "no flags were visited, so this checked nothing")
}

// The placeholder the three --path flags actually render with, spelled out so
// the regression reads as itself rather than as a property of every flag.
func TestPathRendersAsAValueNotAsACommandName(t *testing.T) {
	for _, path := range []string{"init", "generate", "create"} {
		t.Run(path, func(t *testing.T) {
			cmd := find(t, path)
			f := cmd.Flags().Lookup("path")
			if !assert.NotNil(t, f, "%s must accept --path", path) {
				return
			}

			name, usage := pflag.UnquoteUsage(f)

			assert.Equal(t, "string", name, "--path takes a directory, not the literal %q", name)
			assert.Contains(t, usage, "init", "the prose still says where the default comes from")
		})
	}
}
