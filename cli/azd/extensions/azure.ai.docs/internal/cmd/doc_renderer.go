// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_renderer.go produces the styled body and Examples sections for
// `azd ai doc` and `azd ai doc <category>`. The two outputs are split
// so callers can wire them into helpformat.Install's Description and
// Footer slots separately, avoiding the double-Examples render that
// happens when both Description and cmd.Example are set.
//
// Direct invocations (RunE) concatenate Body + Examples to produce the
// same content `--help` shows above its Usage / Flags blocks.

package cmd

import (
	"fmt"
	"strings"

	"azure.ai.docs/internal/helpformat"
)

// renderRootBody returns the rendered body for `azd ai doc`: preamble
// followed by an "Available Documentation:" section listing every
// category with its Short description AND every topic nested under
// each category. This is the comprehensive catalog view -- a single
// invocation shows every doc topic across every group, with per-topic
// descriptions and any References-for-<topic> sub-blocks. Mirrors the
// shape of a `skills` catalog (one screen, all groups, all topics).
//
// The section is named "Available Documentation" (NOT "Available
// Commands") because the docs extension's root cobra command has REAL
// subcommands (agent, skills, version, metadata) that cobra renders
// under its own "Available Commands:" header from the styled
// UsageTemplate. Two sections with the same name in one help output
// would confuse a reader; the rename makes the catalog intent explicit.
func renderRootBody(cats []DocCategory) string {
	var b strings.Builder
	notes := []string{
		helpformat.Note(fmt.Sprintf(
			"Each command group below collects workflow docs an AI coding assistant can "+
				"read directly to drive the matching %s write commands.",
			helpformat.Command("azd ai *"),
		)),
		helpformat.Note("Topic bodies are self-contained markdown -- pipe to a model or print to a terminal."),
	}
	b.WriteString(helpformat.Description(
		"The agent-friendly documentation front door for Azure AI Foundry extensions.",
		notes...,
	))
	b.WriteString(helpformat.SectionHeader("Available Documentation"))
	b.WriteString("\n\n")
	for i, c := range cats {
		// Single header line per category: "  agent  -- <Short description>".
		// We render only Short here -- DisplayName ("Foundry agents
		// (azure.ai.agents)") was previously printed on a separate
		// indented line below, but it overlapped redundantly with
		// Short and added a visual gap that broke the flow. Short
		// owns the per-category one-liner and is authored to include
		// any extension reference inline (see docCategories).
		b.WriteString("  ")
		b.WriteString(helpformat.Command(c.Name))
		b.WriteString("  -- ")
		b.WriteString(c.Short)
		b.WriteString("\n")
		if len(c.Topics) > 0 {
			b.WriteString("\n    Topics:\n")
			width := topicColumnWidth(c.Topics)
			for _, t := range c.Topics {
				b.WriteString("      ")
				b.WriteString(helpformat.Command(t.Name))
				b.WriteString(padRight(t.Name, width))
				b.WriteString(": ")
				b.WriteString(t.Short)
				b.WriteString("\n")
			}
		}
		// One References block per topic that has any, inside the
		// category's block so the reader sees them in context.
		for _, t := range c.Topics {
			if len(t.References) == 0 {
				continue
			}
			b.WriteString("\n    ")
			b.WriteString(helpformat.SectionHeader(fmt.Sprintf("References for `%s`", t.Name)))
			b.WriteString("\n")
			refWidth := referenceColumnWidth(t.References)
			for _, r := range t.References {
				b.WriteString("      ")
				b.WriteString(helpformat.Command(r.Name))
				b.WriteString(padRight(r.Name, refWidth))
				b.WriteString(": ")
				b.WriteString(r.Short)
				b.WriteString("\n")
			}
		}
		if i < len(cats)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// renderRootExamples returns the styled Examples block for the root
// catalog. Command tokens are wrapped in helpformat.Command so they
// render blue; argument placeholders are wrapped in helpformat.Arg
// (yellow). The catalog is the source of truth for the example
// commands; the rendering layer owns the styling.
func renderRootExamples(cats []DocCategory) string {
	samples := map[string]string{
		// Lexical sort: "List ..." (L) < "Print ..." (P). Titles
		// chosen so the sorted order reads as a natural progression.
		"List available documentation groups.": helpformat.Command("azd ai doc"),
	}
	if len(cats) > 0 {
		first := cats[0]
		samples[fmt.Sprintf("List topics for the %s group.", first.Name)] = fmt.Sprintf("%s %s",
			helpformat.Command("azd ai doc"),
			helpformat.Command(first.Name),
		)
		if len(first.Topics) > 0 {
			samples[fmt.Sprintf("Print the %s topic body.", first.Topics[0].Name)] = fmt.Sprintf("%s %s %s",
				helpformat.Command("azd ai doc"),
				helpformat.Command(first.Name),
				helpformat.Command(first.Topics[0].Name),
			)
		}
	}
	return helpformat.Examples(samples)
}

// renderCatalogBody returns the rendered body for `azd ai doc <category>`:
// preamble followed by "Available Commands:" (topics + descriptions)
// followed by optional "References for `<topic>`:" blocks for any topic
// whose References field is non-empty.
//
// "Available Commands:" is safe to use at the category level because
// topics are positional args -- there is no cobra-side Available
// Commands section to conflict with on the agent command.
func renderCatalogBody(cat DocCategory) string {
	var b strings.Builder
	title := fmt.Sprintf("Agent-friendly workflow documentation for the %s extension.",
		categoryExtensionName(cat))
	notes := make([]string, 0, len(cat.Preamble))
	for _, p := range cat.Preamble {
		notes = append(notes, helpformat.Note(p))
	}
	b.WriteString(helpformat.Description(title, notes...))
	b.WriteString(helpformat.SectionHeader("Available Commands"))
	b.WriteString("\n")
	width := topicColumnWidth(cat.Topics)
	for _, t := range cat.Topics {
		b.WriteString("  ")
		b.WriteString(helpformat.Command(t.Name))
		b.WriteString(padRight(t.Name, width))
		b.WriteString(": ")
		b.WriteString(t.Short)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	// One References block per topic that has any. Topics with no
	// references are skipped entirely so a category whose topics all
	// lack references produces no References output.
	for _, t := range cat.Topics {
		if len(t.References) == 0 {
			continue
		}
		b.WriteString(helpformat.SectionHeader(fmt.Sprintf("References for `%s`", t.Name)))
		b.WriteString("\n")
		refWidth := referenceColumnWidth(t.References)
		for _, r := range t.References {
			b.WriteString("  ")
			b.WriteString(helpformat.Command(r.Name))
			b.WriteString(padRight(r.Name, refWidth))
			b.WriteString(": ")
			b.WriteString(r.Short)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderCatalogExamples returns just the styled Examples block for one
// category. Reads cat.Examples (a map of title -> bare command string)
// and wraps each command in helpformat.Command so the output renders
// blue tokens, matching the convention from azd init --help.
//
// The catalog data stores bare commands -- keeping the YAML/literal
// source readable without ANSI escapes -- and the renderer owns the
// styling at call time.
func renderCatalogExamples(cat DocCategory) string {
	if len(cat.Examples) == 0 {
		return ""
	}
	styled := make(map[string]string, len(cat.Examples))
	for title, cmd := range cat.Examples {
		styled[title] = helpformat.Command(cmd)
	}
	return helpformat.Examples(styled)
}

// padRight returns the space padding needed to right-align the colon
// after a name column. Visible-width based -- ANSI escapes around the
// styled name are zero-width on terminals so the visible column still
// aligns with this trivial computation.
func padRight(name string, width int) string {
	if len(name) >= width {
		return ""
	}
	return strings.Repeat(" ", width-len(name))
}

// topicColumnWidth returns the longest topic name across topics, used
// as the right-pad target for the Available Commands list.
func topicColumnWidth(topics []DocTopic) int {
	w := 0
	for _, t := range topics {
		if len(t.Name) > w {
			w = len(t.Name)
		}
	}
	return w
}

// referenceColumnWidth is the per-topic equivalent of topicColumnWidth,
// scoped to one References block so each block aligns independently.
func referenceColumnWidth(refs []DocReference) int {
	w := 0
	for _, r := range refs {
		if len(r.Name) > w {
			w = len(r.Name)
		}
	}
	return w
}

// categoryExtensionName maps a category Name to its full ai.*
// extension identifier used in the preamble sentence. Today
// connections still live under `azd ai agent connection ...` but the
// concept maps to the azure.ai.connections extension (currently a
// stub) once the namespace move lands -- match the eventual name so
// the preamble doesn't churn when commands relocate. Toolboxes live
// inside the agents extension today (no dedicated CLI verb) -- name
// the extension that owns the implementation. Falls back to a generic
// phrasing so a new category that forgets to update this map still
// produces a sensible preamble.
func categoryExtensionName(cat DocCategory) string {
	switch cat.Name {
	case "agent":
		return "azure.ai.agents"
	case "connection":
		return "azure.ai.connections"
	case "toolbox":
		return "azure.ai.agents"
	case "skill":
		return "azure.ai.skills"
	case "routine":
		return "azure.ai.routines"
	default:
		return fmt.Sprintf("azure.ai.%s", cat.Name)
	}
}
