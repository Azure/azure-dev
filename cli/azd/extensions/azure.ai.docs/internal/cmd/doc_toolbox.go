// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_toolbox.go implements `azd ai doc toolbox [topic]` -- prints
// embedded toolbox-friendly markdown from skills/toolbox/*.md. Mirrors
// doc_agent.go and doc_connection.go; all three share printCategoryTopic
// and the embedded skillsFS in doc_agent.go (via the //go:embed
// skills/*/*.md glob).
//
// Add a new topic by dropping a markdown file with front-matter into
// skills/toolbox/; the catalog loader picks it up automatically.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newToolboxCommand returns `azd ai doc toolbox [topic]`. When invoked with
// no positional arg, prints the toolbox topic list. When invoked with a
// positional topic name, prints that topic body.
//
// Acts as a single entry point an agent uses to load just the slice of
// toolbox docs it needs to author the toolboxes[] block of azure.yaml and
// to wire the agent code against the deployed TOOLBOX_<NAME>_MCP_ENDPOINT.
func newToolboxCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "toolbox [topic]",
		Short: "Print agent-friendly documentation for Foundry toolboxes.",
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
				cat := FindCategory("toolbox")
				if cat == nil {
					return fmt.Errorf("doc catalog: toolbox category not registered")
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
			return printCategoryTopic(cmd.OutOrStdout(), "toolbox", args[0])
		},
	}
	return cmd
}
