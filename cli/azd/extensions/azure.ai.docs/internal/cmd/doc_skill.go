// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_skill.go implements `azd ai doc skill [topic]` -- prints
// embedded skill-friendly markdown from skills/skill/*.md. Mirrors
// doc_agent.go / doc_connection.go / doc_toolbox.go; all four share
// printCategoryTopic and the embedded skillsFS in doc_agent.go (via
// the //go:embed skills/*/*.md glob).
//
// These are docs for the Foundry Skill resource (versioned,
// project-scoped behavioral guidelines managed via `azd ai skill`
// from the azure.ai.skills extension). They are intentionally
// distinct from the embedded `azd-ai-skill` pack copied into the
// user's project by `azd ai doc install skill`.
//
// Add a new topic by dropping a markdown file with front-matter into
// skills/skill/; the catalog loader picks it up automatically.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newSkillCommand returns `azd ai doc skill [topic]`. When invoked
// with no positional arg, prints the skill topic list. When invoked
// with a positional topic name, prints that topic body.
//
// Acts as a single entry point an agent uses to load just the slice of
// Foundry skill docs it needs to drive the `azd ai skill` CLI and to
// wire downloaded SKILL.md files into a Hosted agent.
func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill [topic]",
		Short: "Print agent-friendly documentation for Foundry skills.",
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
				cat := FindCategory("skill")
				if cat == nil {
					return fmt.Errorf("doc catalog: skill category not registered")
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
			return printCategoryTopic(cmd.OutOrStdout(), "skill", args[0])
		},
	}
	return cmd
}
