// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_routine.go implements `azd ai doc routine [topic]` -- prints
// embedded routine-friendly markdown from skills/routine/*.md. Mirrors
// doc_agent.go / doc_connection.go / doc_toolbox.go / doc_skill.go; all
// five share printCategoryTopic and the embedded skillsFS in
// doc_agent.go (via the //go:embed skills/*/*.md glob).
//
// These are docs for the Foundry Routine resource (trigger + action
// pairs that fire on a schedule, a one-shot timer, or an external
// event such as a GitHub issue, and invoke an agent on the project).
// Managed via `azd ai routine` from the azure.ai.routines extension.
//
// Add a new topic by dropping a markdown file with front-matter into
// skills/routine/; the catalog loader picks it up automatically.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newRoutineCommand returns `azd ai doc routine [topic]`. When invoked
// with no positional arg, prints the routine topic list. When invoked
// with a positional topic name, prints that topic body.
//
// Acts as a single entry point an agent uses to load just the slice of
// Foundry routine docs it needs to drive the `azd ai routine` CLI and
// to author a routine manifest (trigger + action) for a deployed agent.
func newRoutineCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routine [topic]",
		Short: "Print agent-friendly documentation for Foundry routines.",
		// Long is intentionally empty: the styled Description function
		// passed via helpformat.Install in root.go drives the --help
		// preamble (the same string the RunE prints below for direct
		// invocation). cmd.Example is also intentionally empty so
		// helpformat.Install's cmd.Example auto-migration does not
		// produce a duplicate Examples block alongside the Footer one
		// we wire in root.go.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				cat := FindCategory("routine")
				if cat == nil {
					return fmt.Errorf("doc catalog: routine category not registered")
				}
				out := cmd.OutOrStdout()
				if _, err := fmt.Fprint(out, renderCatalogBody(*cat)); err != nil {
					return err
				}
				if _, err := fmt.Fprint(out, renderCatalogExamples(*cat)); err != nil {
					return err
				}
				return nil
			}
			return printCategoryTopic(cmd.OutOrStdout(), "routine", args[0])
		},
	}
	return cmd
}
