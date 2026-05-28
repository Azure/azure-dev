// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentVariablesSection_HasExpectedKeys(t *testing.T) {
	got := environmentVariablesSection()
	// These names are the wire contract -- if any rename, doctor and the
	// project resolver also need updating, so a test pin prevents drift.
	for _, want := range []string{
		"Environments & Environment Variables:",
		"azd env list",
		"azd env new",
		"azd env select",
		"azd env get",
		"azd env set",
		"AZURE_SUBSCRIPTION_ID",
		"AZURE_LOCATION",
		"AZURE_AI_PROJECT_ENDPOINT",
		"FOUNDRY_PROJECT_ENDPOINT",
		"AZURE_AI_PROJECT_ID",
		"AGENT_<SVC>_<PROTO>_ENDPOINT",
		"AGENT_<SVC>_ENDPOINT",
	} {
		assert.True(t, strings.Contains(got, want),
			"Environments & Environment Variables section missing %q", want)
	}
}

// TestEnvironmentVariablesSection_HeaderUnderlined confirms the header
// renders with the SAME bold+underline styling as the Install-managed
// sections (Usage, Available Commands, Flags, Global Flags). Before
// the SectionHeader migration it was bold-only -- visually inconsistent
// with the styled middle of --help.
func TestEnvironmentVariablesSection_HeaderUnderlined(t *testing.T) {
	withColorEnabledLocal(t)
	got := environmentVariablesSection()
	// Underline attribute is ESC[4m; bold is ESC[1m. fatih/color may emit
	// either order, so just assert both attributes appear ahead of the
	// header text.
	require.Contains(t, got, "Environments & Environment Variables:")
	require.Contains(t, got, "\x1b[", "expected ANSI escape sequences around header")
	require.Contains(t, got, "4m", "expected underline attribute on header")
}

func TestDocsAndAgentSkillsSection_ListsAgentReadCommands(t *testing.T) {
	got := docsAndAgentSkillsSection()
	for _, want := range []string{
		"Docs & Agent Skills:",
		"azd ai agent show",
		"azd ai project show",
		"azd ai agent doctor",
		"azd ext install azure.ai.docs",
		"azd ai doc",
		"azd ai doc agent",
	} {
		assert.True(t, strings.Contains(got, want),
			"DOCS section missing %q", want)
	}
}

// TestDocsAndAgentSkillsSection_HeaderUnderlined is the docs-section
// mirror of TestEnvironmentVariablesSection_HeaderUnderlined.
func TestDocsAndAgentSkillsSection_HeaderUnderlined(t *testing.T) {
	withColorEnabledLocal(t)
	got := docsAndAgentSkillsSection()
	require.Contains(t, got, "Docs & Agent Skills:")
	require.Contains(t, got, "\x1b[", "expected ANSI escape sequences around header")
	require.Contains(t, got, "4m", "expected underline attribute on header")
}

// withColorEnabledLocal temporarily forces color.NoColor=false so a
// styling test can assert the escape codes that fatih/color emits.
// Must NOT be combined with t.Parallel -- color.NoColor is process-
// global state.
func withColorEnabledLocal(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })
}

func TestFormatGetStarted_RendersHeaderAndLines(t *testing.T) {
	got := formatGetStarted("Header here:", "first  Description 1.", "second  Description 2.")
	assert.True(t, strings.Contains(got, "Header here:"), "header missing")
	assert.True(t, strings.Contains(got, "first  Description 1."), "first line missing")
	assert.True(t, strings.Contains(got, "second  Description 2."), "second line missing")
}

func TestFindAzureYaml_NotFound_ReturnsFalseInTempDir(t *testing.T) {
	// Run from a directory guaranteed to be outside any azd project: t.TempDir.
	// chdir-isolation is t.Chdir's whole job.
	t.Chdir(t.TempDir())
	_, found := findAzureYaml()
	assert.False(t, found, "findAzureYaml should return false in an empty temp dir")
}

// TestInstallAgentsHelpOutput_DescriptionBeforePreambleBeforeUsage pins the
// section order on the root command's --help:
//
//  1. cobra Short description ("Ship agents ...")
//  2. state-aware "Get started" preamble
//  3. Usage block
//
// Also asserts a single blank line between each section (regression guard for
// the prior Fprintln-vs-Fprint spacing bug).
func TestInstallAgentsHelpOutput_DescriptionBeforePreambleBeforeUsage(t *testing.T) {
	// t.Chdir to a fresh temp dir so findAzureYaml returns false and the
	// deterministic "No azd project detected" preamble fires -- no azd client.
	t.Chdir(t.TempDir())

	rootCmd := &cobra.Command{
		Use:   "agent",
		Short: "COBRABODY-MARKER",
	}
	installAgentsHelpOutput(rootCmd)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	require.NoError(t, rootCmd.Help())

	output := buf.String()

	descIdx := strings.Index(output, "COBRABODY-MARKER")
	require.GreaterOrEqual(t, descIdx, 0, "Short description not found in output:\n%s", output)

	preambleIdx := strings.Index(output, "No azd project detected.")
	require.GreaterOrEqual(t, preambleIdx, 0, "preamble not found in output:\n%s", output)

	usageIdx := strings.Index(output, "Usage:")
	require.GreaterOrEqual(t, usageIdx, 0, "Usage block not found in output:\n%s", output)

	// Order: description -> preamble -> Usage.
	assert.Less(t, descIdx, preambleIdx, "Short description should appear before preamble")
	assert.Less(t, preambleIdx, usageIdx, "preamble should appear before Usage block")

	// One blank line (= exactly 2 newlines) between description and preamble.
	gap := output[descIdx+len("COBRABODY-MARKER") : preambleIdx]
	assert.Equal(t, "", strings.TrimSpace(gap), "unexpected non-whitespace between description and preamble: %q", gap)
	assert.Equal(t, 2, strings.Count(gap, "\n"),
		"expected 1 blank line between description and preamble, got %q", gap)

	// One blank line between preamble's last visible text and the Usage block.
	const preambleTail = "agent project."
	tailIdx := strings.Index(output, preambleTail)
	require.GreaterOrEqual(t, tailIdx, 0, "preamble tail %q not found", preambleTail)
	tailIdx += len(preambleTail)
	gap = output[tailIdx:usageIdx]
	assert.Equal(t, "", strings.TrimSpace(gap), "unexpected non-whitespace between preamble and Usage: %q", gap)
	assert.Equal(t, 2, strings.Count(gap, "\n"),
		"expected 1 blank line between preamble and Usage, got %q", gap)
}
