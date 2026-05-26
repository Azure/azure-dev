// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_agent.go implements `azd ai doc agent [topic]` -- prints embedded
// agent-friendly markdown from skills/agent/*.md. The markdown is owned
// by (and lives in) this extension; each topic is a self-contained
// contract the agent reads to drive `azd ai agent` write commands.
//
// Per-extension topic folders live at skills/<sibling>/. As other ai.*
// extensions get their own topic sets, add a sibling subdir and a
// matching subcommand here (newToolboxCommand, newProjectCommand, etc.).

package cmd

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// skillsFS embeds every topic markdown shipped by this extension. Add a
// new sibling-extension topic group by creating skills/<sibling>/<topic>.md
// files; the listTopics helper picks them up automatically.
//
//go:embed skills/*/*.md
var skillsFS embed.FS

const skillsRoot = "skills"

// newAgentCommand returns `azd ai doc agent [topic]`. When invoked with
// no positional arg, prints the agent-extension topic list. When invoked
// with a positional topic name, prints that topic body.
//
// Acts as a single entry point an agent uses to load just the slice of
// docs it needs to drive the matching CLI commands.
func newAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent [topic]",
		Short: "Print agent-friendly documentation for the azure.ai.agents extension.",
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
				cat := FindCategory("agent")
				if cat == nil {
					return fmt.Errorf("doc catalog: agent category not registered")
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
			return printCategoryTopic(cmd.OutOrStdout(), "agent", args[0])
		},
	}
	return cmd
}

// printCategoryTopic prints the markdown body for one topic. Unknown
// topics return an error that lists the valid topics, so an agent that
// mistypes a topic can self-correct without a doc lookup.
//
// The body is the source file with its YAML front-matter block
// stripped (via stripFrontMatter). The stripped output is byte-
// identical to the source from the post-fence position through EOF,
// pinned by TestStripFrontMatter_PreservesBodyByteForByte.
func printCategoryTopic(w io.Writer, category, topic string) error {
	path := fmt.Sprintf("%s/%s.md", categoryDir(category), topic)
	raw, err := fs.ReadFile(skillsFS, path)
	if err != nil {
		known, _ := readCategoryTopicNames(category)
		return fmt.Errorf(
			"unknown topic %q. Valid topics: %s",
			topic, strings.Join(known, ", "))
	}
	body := stripFrontMatter(raw)

	if _, err := w.Write(body); err != nil {
		return err
	}
	// Trailing newline so terminal users get a clean prompt back.
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = w.Write([]byte{'\n'})
	}
	return nil
}

// categoryDir returns the embedded-FS directory for a sibling-extension
// topic group. Centralized so a future tweak to the layout only changes
// one line.
func categoryDir(category string) string {
	return skillsRoot + "/" + category
}

// readCategoryTopicNames returns the sorted topic names for a category.
// Used by printCategoryTopic to render a helpful "did you mean" list when
// a topic name is unknown.
func readCategoryTopicNames(category string) ([]string, error) {
	entries, err := fs.ReadDir(skillsFS, categoryDir(category))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(names)
	return names, nil
}
