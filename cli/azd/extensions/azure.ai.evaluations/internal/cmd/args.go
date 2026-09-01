// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
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

// Paging is one contract across every listing, so a reader who learns it on one
// command has learned it on all of them.
//
// --limit alone truncates rather than paginates: it caps the rows and says
// nothing about what is past them, which is what the two commands that had it
// were doing. A page is only usable if the next one is reachable, so the token
// the service returned is printed with the command that resumes from it, and
// --all is the explicit way to ask for everything rather than the accident of
// a listing that never stopped.
func addPagingFlags(cmd *cobra.Command, limit *int, token *string, all *bool, defaultPage int) {
	cmd.Flags().IntVar(limit, "limit", 0,
		fmt.Sprintf("Rows per page. Defaults to %d.", defaultPage))
	cmd.Flags().StringVar(token, "continuation-token", "",
		"Resume from the token the previous page printed.")
	cmd.Flags().BoolVar(all, "all", false,
		"Retrieve every page. Overrides --limit.")
}

// pageSizeOr settles the page size a listing asks the service for.
//
// --all is not "a very large limit": it means follow the cursor, so it asks for
// no limit at all and lets the walker finish.
func pageSizeOr(limit int, all bool, fallback int) int {
	if all {
		return 0
	}
	if limit > 0 {
		return limit
	}
	return fallback
}

// defaultPageSize keeps a first page quick on a shared project, which runs to
// hundreds of evals and datasets. Walking all of them took seconds and buried
// the reader's own rows.
const defaultPageSize = 50
