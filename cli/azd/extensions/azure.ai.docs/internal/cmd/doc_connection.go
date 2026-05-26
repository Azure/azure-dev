// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_connection.go implements `azd ai doc connection [topic]` -- prints
// embedded connection-friendly markdown from skills/connection/*.md. Mirrors
// the structure of doc_agent.go; both share printCategoryTopic and the
// embedded skillsFS in doc_agent.go (via the //go:embed skills/*/*.md glob).
//
// Add a new topic by dropping a markdown file with front-matter into
// skills/connection/; the catalog loader picks it up automatically.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newConnectionCommand returns `azd ai doc connection [topic]`. When invoked
// with no positional arg, prints the connection topic list. When invoked with
// a positional topic name, prints that topic body.
//
// Acts as a single entry point an agent uses to load just the slice of
// connection docs it needs to drive `azd ai agent connection` and to author
// the connections / toolConnections blocks of azure.yaml.
func newConnectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connection [topic]",
		Short: "Print agent-friendly documentation for Foundry project connections.",
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
				cat := FindCategory("connection")
				if cat == nil {
					return fmt.Errorf("doc catalog: connection category not registered")
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
			return printCategoryTopic(cmd.OutOrStdout(), "connection", args[0])
		},
	}
	return cmd
}
