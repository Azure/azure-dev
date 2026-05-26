// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withColorOff disables ANSI color output for the duration of one test.
// MUST NOT be combined with t.Parallel: color.NoColor is process-global.
// Local copy here so renderer tests don't depend on the integration-test
// helper's lifetime.
func withColorOff(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

func TestRenderRootBody_HasAvailableDocumentationHeader(t *testing.T) {
	withColorOff(t)
	got := renderRootBody(docCategories)
	assert.Contains(t, got, "Available Documentation:",
		"root catalog must use the renamed header to avoid colliding with "+
			"cobra's Available Commands list (which lists agent/skills/version/metadata)")
	assert.NotContains(t, got, "Available Commands:",
		"root must NOT render Available Commands -- that's cobra's section name for the subcommand list")
}

func TestRenderRootBody_IncludesAgentRow(t *testing.T) {
	withColorOff(t)
	got := renderRootBody(docCategories)
	assert.Contains(t, got, "agent")
	assert.Contains(t, got, "Foundry agents")
}

func TestRenderRootBody_IncludesPreambleBullets(t *testing.T) {
	withColorOff(t)
	got := renderRootBody(docCategories)
	// Bullets are emitted via helpformat.Note ("  * <text>").
	assert.Contains(t, got, "  * ", "expected at least one bullet in the preamble")
}

func TestRenderCatalogBody_TopicsInWorkflowOrder(t *testing.T) {
	withColorOff(t)
	cat := FindCategory("agent")
	require.NotNil(t, cat)
	got := renderCatalogBody(*cat)

	// Locate each topic's row by its leading "  <name>" prefix; assert
	// they appear in the workflow order locked by Decision #2.
	initIdx := strings.Index(got, "initialize")
	cfgIdx := strings.Index(got, "configure")
	opIdx := strings.Index(got, "operate")
	invIdx := strings.Index(got, "investigate")

	require.Positive(t, initIdx, "initialize missing")
	require.Positive(t, cfgIdx, "configure missing")
	require.Positive(t, opIdx, "operate missing")
	require.Positive(t, invIdx, "investigate missing")

	require.Less(t, initIdx, cfgIdx, "initialize must appear before configure")
	require.Less(t, cfgIdx, opIdx, "configure must appear before operate")
	require.Less(t, opIdx, invIdx, "operate must appear before investigate")
}

func TestRenderCatalogBody_IncludesAvailableCommandsHeader(t *testing.T) {
	withColorOff(t)
	cat := FindCategory("agent")
	require.NotNil(t, cat)
	got := renderCatalogBody(*cat)
	assert.Contains(t, got, "Available Commands:",
		"category body uses Available Commands (safe -- topics are positional args, no cobra collision)")
}

func TestRenderCatalogBody_OmitsReferencesWhenAllTopicsHaveNone(t *testing.T) {
	withColorOff(t)
	cat := FindCategory("agent")
	require.NotNil(t, cat)
	// Shipped agent topics have no References today.
	got := renderCatalogBody(*cat)
	assert.NotContains(t, got, "References for ",
		"References section must be entirely omitted when no topic has references")
}

// TestRenderCatalogBody_RendersReferencesWhenPresent uses synthetic
// data so the shipped topics need no `references:` entries.
func TestRenderCatalogBody_RendersReferencesWhenPresent(t *testing.T) {
	withColorOff(t)
	synthetic := DocCategory{
		Name:        "synth",
		DisplayName: "Synthetic",
		Short:       "Synthetic category for testing.",
		Preamble:    []string{"Preamble bullet."},
		Topics: []DocTopic{
			{
				Name:  "configure",
				Short: "Configure things.",
				Order: 10,
				References: []DocReference{
					{Name: "role-assignments", Short: "Manage role-based access."},
					{Name: "connections", Short: "Manage Foundry connections."},
				},
			},
		},
		Examples: map[string]string{},
	}
	got := renderCatalogBody(synthetic)
	assert.Contains(t, got, "References for `configure`:",
		"References block header must be rendered with the topic name")
	assert.Contains(t, got, "role-assignments")
	assert.Contains(t, got, "Manage role-based access.")
	assert.Contains(t, got, "connections")
	assert.Contains(t, got, "Manage Foundry connections.")
}

func TestRenderRootExamples_ReturnsOnlyExamplesBlock(t *testing.T) {
	withColorOff(t)
	got := renderRootExamples(docCategories)
	assert.Contains(t, got, "Examples:")
	assert.NotContains(t, got, "Available Documentation:")
	assert.NotContains(t, got, "Available Commands:")
}

func TestRenderCatalogExamples_ReturnsOnlyExamplesBlock(t *testing.T) {
	withColorOff(t)
	cat := FindCategory("agent")
	require.NotNil(t, cat)
	got := renderCatalogExamples(*cat)
	assert.Contains(t, got, "Examples:")
	assert.NotContains(t, got, "Available Commands:")
}

func TestRenderCatalogExamples_EmptyExamplesYieldsEmptyString(t *testing.T) {
	withColorOff(t)
	cat := DocCategory{Name: "x", Examples: nil}
	got := renderCatalogExamples(cat)
	assert.Equal(t, "", got, "no examples -> empty string (no Examples: header)")
}

// TestRenderRootExamples_StylesCommandTokens is the regression for the
// user-reported issue that `azd ai doc` and `azd ai doc --help`
// rendered Examples commands as plain text. With color forced on, the
// example command bytes must include ANSI escape sequences -- otherwise
// the catalog Examples have lost their blue command coloring.
func TestRenderRootExamples_StylesCommandTokens(t *testing.T) {
	withColorOn(t)
	got := renderRootExamples(docCategories)
	require.NotEmpty(t, got)
	require.Contains(t, got, "\x1b[", "expected ANSI escapes around example command tokens")
}

func TestRenderCatalogExamples_StylesCommandTokens(t *testing.T) {
	withColorOn(t)
	cat := FindCategory("agent")
	require.NotNil(t, cat)
	got := renderCatalogExamples(*cat)
	require.NotEmpty(t, got)
	require.Contains(t, got, "\x1b[", "expected ANSI escapes around example command tokens")
}

// TestRenderRootBody_NestsTopicsUnderEachCategory pins the
// comprehensive-catalog layout: the root view shows each category's
// topics inline (not just the category name + short). User feedback:
// the old single-row layout was too minimal compared to a
// `skills`-catalog style listing.
func TestRenderRootBody_NestsTopicsUnderEachCategory(t *testing.T) {
	withColorOff(t)
	got := renderRootBody(docCategories)
	assert.Contains(t, got, "Topics:",
		"root body must include a per-category Topics: block")
	for _, want := range []string{"initialize", "configure", "operate", "investigate"} {
		assert.Contains(t, got, want, "topic %q missing from root catalog", want)
	}
}

// withColorOn is the inverse of withColorOff: forces color.NoColor=false
// so a styling test can assert escape codes. Same parallel caveat as
// withColorOff -- do not combine with t.Parallel.
func withColorOn(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })
}
