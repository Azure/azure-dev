// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
)

// addRunFlag registers the run selector every run-scoped command shares.
//
// These commands fall back to the run this environment last started, and the
// line disclosing that names this flag -- so a command that could print it and
// not accept it would send the reader to a flag that is not there.
func addRunFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "run", "",
		"Run to act on. Defaults to the last run recorded for --eval.")
}

// requiredArgs is cobra.ExactArgs that says which argument is missing.
//
// Cobra answers "accepts 1 arg(s), received 0", which names neither the
// argument nor where a value comes from. The names come from the command's own
// Use line, so a command cannot document one thing and demand another.
func requiredArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			if len(args) > n {
				return messages.TooManyArgs(cmd.CommandPath(), argNames(cmd), len(args))
			}
			return nil
		}
		return messages.MissingArgs(cmd.CommandPath(), missingFrom(argNames(cmd), len(args)))
	}
}

// argNames reads the placeholders out of a Use line: "show <output-item>"
// yields ["output-item"]. Optional placeholders, written [run], are not
// required and are left out.
func argNames(cmd *cobra.Command) []string {
	var names []string
	for _, field := range strings.Fields(cmd.Use)[1:] {
		if strings.HasPrefix(field, "<") && strings.HasSuffix(field, ">") {
			names = append(names, strings.Trim(field, "<>"))
		}
	}
	return names
}

// missingFrom names the placeholders the caller did not supply.
func missingFrom(names []string, given int) []string {
	if given >= len(names) {
		return nil
	}
	return names[given:]
}
