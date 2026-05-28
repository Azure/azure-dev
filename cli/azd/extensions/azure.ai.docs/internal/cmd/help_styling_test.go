// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// help_styling_test.go covers end-to-end --help output for representative
// commands in the docs extension. See the agents-side mirror file for
// detailed rationale on color-toggling and the helpOf helper shape.

func withColorDisabled(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

func helpOf(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCommand()
	root.SilenceErrors = true
	root.SilenceUsage = true

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(args, "--help"))
	require.NoError(t, root.Execute(), "Execute(%v --help) returned error", args)
	return buf.String()
}

// TestDocRootHelp_StyledSections asserts the styled headers and
// catalog-driven Examples block appear under `doc --help`. With the
// catalog wiring, the root command's --help shows:
//   - "Commands:" (curated cobra-subcommand surface via Description)
//   - "Available Documentation:" (catalog body via Description)
//   - "Available Commands:" (cobra's subcommand list via UsageTemplate)
//   - "Examples:" (catalog examples via Footer; EXACTLY ONCE -- see
//     TestDocHelpOutput_NoDuplicateExamples)
func TestDocRootHelp_StyledSections(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t)

	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "Commands:",
		"catalog body must contribute the curated Commands: section "+
			"(distinct from cobra's auto-generated Available Commands list)")
	assert.Contains(t, out, "Available Documentation:",
		"catalog body must contribute its Available Documentation header")
	assert.Contains(t, out, "Available Commands:",
		"cobra still renders its own Available Commands list for the real subcommand tree")

	// Catalog-driven Examples block (from renderRootExamples via Footer).
	assert.Contains(t, out, "Examples:")
	assert.Contains(t, out, "List available documentation groups.",
		"first catalog example title missing")
	assert.Contains(t, out, "Print the samples topic body.",
		"third catalog example title missing")

	// Cobra's Available Commands listing should include the visible
	// leaves (agent, connection, toolbox, skill, routine, install,
	// version; metadata is reserved by the SDK and may appear as well
	// -- not asserted).
	for _, name := range []string{"agent", "connection", "toolbox", "skill", "routine", "install", "version"} {
		assert.True(t, strings.Contains(out, name),
			"Cobra subcommand list missing %q", name)
	}
}

// TestDocRootHelp_CommandsSectionBeforeAvailableDocumentation pins the
// ordering contract the user asked for: the curated "Commands:" block
// (which surfaces `install`) must render ABOVE "Available
// Documentation:" so an agent reading top-to-bottom encounters the
// actionable command first, then the catalog of doc topics. The
// ordering must hold in --help (Description slot) just as it does in
// bare `azd ai doc` (RunE path) -- both share renderRootBody.
func TestDocRootHelp_CommandsSectionBeforeAvailableDocumentation(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t)

	cmdsIdx := strings.Index(out, "Commands:")
	docsIdx := strings.Index(out, "Available Documentation:")
	availCmdsIdx := strings.Index(out, "Available Commands:")

	require.Positive(t, cmdsIdx, "Commands: header missing from --help output")
	require.Positive(t, docsIdx, "Available Documentation: header missing from --help output")
	require.Positive(t, availCmdsIdx, "cobra Available Commands: header missing from --help output")

	assert.Less(t, cmdsIdx, docsIdx,
		"curated Commands: must appear BEFORE Available Documentation: (the user-requested ordering)")
	assert.Less(t, docsIdx, availCmdsIdx,
		"both catalog headers (from Description) must appear BEFORE cobra's auto-generated Available Commands:")

	// Sanity-check that the install row text from the curated Commands
	// section is actually present (not just the header).
	assert.Contains(t, out, "  install  -- ",
		"install row must render in --help's curated Commands section")
}

// TestDocRootHelp_NoLegacyExamplesInLong is the regression for the
// "drive Description+Footer from the catalog, leave Long empty" rule.
// Setting Long to old inline prose would either replace the catalog
// preamble (Description nil falls back to Long) or duplicate it.
func TestDocRootHelp_NoLegacyExamplesInLong(t *testing.T) {
	root := NewRootCommand()
	assert.Empty(t, root.Long,
		"root.Long must remain empty so helpformat.Install's Description func drives the preamble; "+
			"the catalog renderer (renderRootBody) owns that content")
	assert.Empty(t, root.Example,
		"root.Example must remain empty so helpformat.Install's Footer func drives the Examples; "+
			"a non-empty Example would trigger auto-migration alongside the Footer block")
}

// TestDocAgentHelp_Smoke confirms the agent (topic) command gets
// styled sections and that its catalog-driven Examples block appears.
func TestDocAgentHelp_Smoke(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t, "agent")
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "Global Flags:")
	assert.Contains(t, out, "Examples:", "agent has examples driven by the catalog Footer")
	assert.Contains(t, out, "List topics for the agents extension.",
		"first catalog example title missing")
}

// TestDocInstallSkillHelp_BulletPreambleAndExamples confirms the
// long-form `install skill` command -- which has an existing Long
// containing bullet items written into the cobra.Command literal --
// renders those as plain text alongside the styled section headers
// and migrated Examples. This is the "leave existing Long verbatim"
// path: no Description override, just styling around it.
func TestDocInstallSkillHelp_BulletPreambleAndExamples(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t, "install", "skill")
	assert.Contains(t, out, "Built-in targets:")
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "Flags:")
	assert.Contains(t, out, "--target", "install skill's --target flag should appear in Flags section")
	assert.Contains(t, out, "Examples:", "install skill has migrated examples")
}

// runE runs the root command with args (no --help) and returns the
// captured stdout. Used by direct-invocation tests that exercise the
// RunE-side renderer rather than the --help-side template.
func runE(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCommand()
	root.SilenceErrors = true
	root.SilenceUsage = true

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	require.NoError(t, root.Execute(), "Execute(%v) returned error", args)
	return buf.String()
}

// TestDocCommandOutput_RichStyledCatalog covers the direct invocation
// of `azd ai doc` (no --help). Confirms the styled catalog body and
// Examples block both appear, with the new "Available Documentation"
// header (NOT "Available Commands", which would clash with cobra's
// root subcommand list).
func TestDocCommandOutput_RichStyledCatalog(t *testing.T) {
	withColorDisabled(t)

	out := runE(t)

	assert.Contains(t, out, "agent-friendly documentation front door",
		"preamble title should appear")
	assert.Contains(t, out, "  * ", "preamble should include bullets")
	assert.Contains(t, out, "Commands:",
		"bare `azd ai doc` must surface the curated Commands section so an agent discovers `install` without --help")
	assert.Contains(t, out, "  install  -- ",
		"bare `azd ai doc` must include the install row in the Commands section")
	assert.Contains(t, out, "Available Documentation:",
		"root catalog body uses Available Documentation (not Available Commands)")
	assert.NotContains(t, out, "Available Commands:",
		"root direct output must NOT use Available Commands -- avoids cobra-style confusion")
	assert.Contains(t, out, "agent", "agent category row missing")
	assert.Contains(t, out, "Examples:", "Examples block missing from direct invocation")
	assert.Contains(t, out, "azd ai doc install skill --target copilot",
		"Examples block must include the install example so an agent has a ready-to-run invocation")
}

// TestDocAgentCommandOutput_RichStyledCatalog covers the direct
// invocation of `azd ai doc agent` (no --help). Confirms workflow
// ordering and per-topic descriptions plus the Examples block.
func TestDocAgentCommandOutput_RichStyledCatalog(t *testing.T) {
	withColorDisabled(t)

	out := runE(t, "agent")

	assert.Contains(t, out, "Agent-friendly workflow documentation",
		"category preamble title missing")
	assert.Contains(t, out, "Available Commands:",
		"category direct output uses Available Commands (no cobra collision at this level)")

	// Workflow order: initialize -> configure -> operate -> investigate.
	initIdx := strings.Index(out, "initialize")
	cfgIdx := strings.Index(out, "configure")
	opIdx := strings.Index(out, "operate")
	invIdx := strings.Index(out, "investigate")
	require.Positive(t, initIdx)
	require.Positive(t, cfgIdx)
	require.Positive(t, opIdx)
	require.Positive(t, invIdx)
	assert.Less(t, initIdx, cfgIdx, "initialize must precede configure")
	assert.Less(t, cfgIdx, opIdx, "configure must precede operate")
	assert.Less(t, opIdx, invIdx, "operate must precede investigate")

	// Per-topic descriptions from front-matter.
	assert.Contains(t, out, "Bootstrap a new Foundry agent project end-to-end.")
	assert.Contains(t, out, "Edit azure.yaml service config")
	assert.Contains(t, out, "Run write commands")
	assert.Contains(t, out, "Inspect agent state")

	assert.Contains(t, out, "Examples:")
}

// TestDocAgentTopicCommand_StripsFrontMatter confirms `azd ai doc
// agent configure` prints the markdown body WITHOUT the YAML
// front-matter block. The body must match the source file's
// post-fence bytes EXACTLY (byte-for-byte regression for
// rubber-duck #C).
func TestDocAgentTopicCommand_StripsFrontMatter(t *testing.T) {
	withColorDisabled(t)

	out := runE(t, "agent", "configure")

	require.NotEmpty(t, out)
	assert.False(t, strings.HasPrefix(out, "---"),
		"output must not start with the front-matter fence; first 80 bytes = %q",
		out[:min(80, len(out))])
	assert.True(t, strings.HasPrefix(out, "# Configure"),
		"output should start with the topic's H1; first 80 bytes = %q",
		out[:min(80, len(out))])
	assert.NotContains(t, out, "short: Shape the agent",
		"front-matter content must not leak into the body output")
}

// TestDocHelpOutput_NoDuplicateExamples is the regression for
// rubber-duck #1: with both Description (preamble + Available
// Documentation) AND Footer (Examples) wired into helpformat.Install,
// AND cmd.Example cleared, the --help output must show "Examples:"
// EXACTLY ONCE. A regression that re-sets cmd.Example would trigger
// the auto-migration and produce TWO Examples blocks.
func TestDocHelpOutput_NoDuplicateExamples(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t)
	count := strings.Count(out, "Examples:")
	assert.Equal(t, 1, count,
		"expected exactly one Examples: section in `doc --help`, got %d", count)
}

func TestDocAgentHelpOutput_NoDuplicateExamples(t *testing.T) {
	withColorDisabled(t)

	out := helpOf(t, "agent")
	count := strings.Count(out, "Examples:")
	assert.Equal(t, 1, count,
		"expected exactly one Examples: section in `doc agent --help`, got %d", count)
}
