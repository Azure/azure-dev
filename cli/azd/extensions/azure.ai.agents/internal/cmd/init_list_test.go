// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureTemplates returns a small but representative mix used across the
// init-list tests: featured/recommended Python agent, featured C# agent,
// non-featured Python azd-type, and a recommended-only Python agent.
func fixtureTemplates() []AgentTemplate {
	return []AgentTemplate{
		{
			Title:         "Echo Agent",
			Description:   "An agent that echoes input.",
			Languages:     []string{"python"},
			Source:        "https://github.com/org/repo/blob/main/echo/agent.yaml",
			ExtensionTags: []string{"featured", "recommended"},
			TemplateType:  "extension.ai.agent",
		},
		{
			Title:         "Calculator Agent",
			Description:   "A calculator agent.",
			Languages:     []string{"dotnetCsharp"},
			Source:        "https://github.com/org/repo/blob/main/calc/agent.manifest.yaml",
			ExtensionTags: []string{"featured"},
			TemplateType:  "extension.ai.agent",
		},
		{
			Title:         "Full Stack Starter",
			Description:   "A full azd template repo.",
			Languages:     []string{"python"},
			Source:        "Azure-Samples/azd-agent-starter",
			ExtensionTags: nil,
			TemplateType:  "extension.ai.agent",
		},
		{
			Title:         "Recommended Only",
			Description:   "Marked recommended without featured.",
			Languages:     []string{"python"},
			Source:        "https://example.com/agents/rec/agent.yaml",
			ExtensionTags: []string{"recommended"},
			TemplateType:  "extension.ai.agent",
		},
	}
}

func TestValidateInitListFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   initListFlags
		wantErr bool
		errMsg  string
	}{
		{name: "all empty is valid", flags: initListFlags{}, wantErr: false},
		{name: "known language", flags: initListFlags{language: "python"}, wantErr: false},
		{name: "known type agent", flags: initListFlags{templateType: TemplateTypeAgent}, wantErr: false},
		{name: "known type azd", flags: initListFlags{templateType: TemplateTypeAzd}, wantErr: false},
		{name: "unknown language", flags: initListFlags{language: "rust"}, wantErr: true, errMsg: `unknown language "rust"`},
		{name: "unknown type", flags: initListFlags{templateType: "bogus"}, wantErr: true, errMsg: `unknown template type "bogus"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateInitListFlags(&tc.flags)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMapAgentTemplateToDTO_AgentType(t *testing.T) {
	t.Parallel()

	src := "https://github.com/org/repo/blob/main/echo/agent.yaml"
	in := AgentTemplate{
		Title:         "Echo Agent",
		Description:   "An agent that echoes input.",
		Languages:     []string{"python"},
		Source:        src,
		ExtensionTags: []string{"featured", "recommended"},
	}

	got := mapAgentTemplateToDTO(in)

	require.Equal(t, "Echo Agent", got.Title)
	require.Equal(t, "An agent that echoes input.", got.Description)
	require.Equal(t, []string{"python"}, got.Languages)
	require.Equal(t, TemplateTypeAgent, got.Type)
	require.Equal(t, src, got.ManifestURL)
	require.Empty(t, got.RepoURL, "RepoURL must be empty for agent type")
	require.Equal(t, []string{"featured", "recommended"}, got.Tags)
	require.True(t, got.Featured)
	require.True(t, got.Recommended)
	require.Equal(t, "azd ai agent init -m "+src, got.InitCommand)
}

func TestMapAgentTemplateToDTO_AzdType(t *testing.T) {
	t.Parallel()

	src := "Azure-Samples/azd-agent-starter"
	in := AgentTemplate{
		Title:         "Full Stack Starter",
		Description:   "A full azd template repo.",
		Languages:     []string{"python"},
		Source:        src,
		ExtensionTags: nil,
	}

	got := mapAgentTemplateToDTO(in)

	require.Equal(t, TemplateTypeAzd, got.Type)
	require.Empty(t, got.ManifestURL, "ManifestURL must be empty for azd type")
	require.Equal(t, src, got.RepoURL)
	require.False(t, got.Featured)
	require.False(t, got.Recommended)
	require.Equal(t, "azd init -t "+src, got.InitCommand)
}

func TestMapAgentTemplateToDTO_ManifestUrlAndRepoUrlAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	// Critical contract: consumers branch on which URL is populated.
	for _, tmpl := range fixtureTemplates() {
		got := mapAgentTemplateToDTO(tmpl)
		hasManifest := got.ManifestURL != ""
		hasRepo := got.RepoURL != ""
		require.True(t, hasManifest != hasRepo,
			"exactly one of ManifestURL/RepoURL must be set for %q (got manifest=%q repo=%q)",
			got.Title, got.ManifestURL, got.RepoURL)
	}
}

func TestBuildTemplateListItems_NoFilters(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{})

	require.Len(t, items, 4)
	// Featured first, alphabetical within group.
	require.Equal(t, "Calculator Agent", items[0].Title)
	require.True(t, items[0].Featured)
	require.Equal(t, "Echo Agent", items[1].Title)
	require.True(t, items[1].Featured)
	// Non-featured in alphabetical order after featured group.
	require.Equal(t, "Full Stack Starter", items[2].Title)
	require.False(t, items[2].Featured)
	require.Equal(t, "Recommended Only", items[3].Title)
	require.False(t, items[3].Featured)
}

func TestBuildTemplateListItems_LanguageFilter(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{language: "python"})

	require.Len(t, items, 3)
	for _, it := range items {
		require.Contains(t, it.Languages, "python")
	}

	csItems := buildTemplateListItems(fixtureTemplates(), &initListFlags{language: "dotnetCsharp"})
	require.Len(t, csItems, 1)
	require.Equal(t, "Calculator Agent", csItems[0].Title)
}

func TestBuildTemplateListItems_FeaturedOnly(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{featuredOnly: true})

	require.Len(t, items, 2)
	for _, it := range items {
		require.True(t, it.Featured, "featured-only filter must drop non-featured entries")
	}
}

func TestBuildTemplateListItems_TypeFilter(t *testing.T) {
	t.Parallel()

	agentItems := buildTemplateListItems(fixtureTemplates(), &initListFlags{templateType: TemplateTypeAgent})
	require.Len(t, agentItems, 3)
	for _, it := range agentItems {
		require.Equal(t, TemplateTypeAgent, it.Type)
		require.NotEmpty(t, it.ManifestURL)
	}

	azdItems := buildTemplateListItems(fixtureTemplates(), &initListFlags{templateType: TemplateTypeAzd})
	require.Len(t, azdItems, 1)
	require.Equal(t, TemplateTypeAzd, azdItems[0].Type)
	require.NotEmpty(t, azdItems[0].RepoURL)
}

func TestBuildTemplateListItems_CombinedFilters(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{
		language:     "python",
		featuredOnly: true,
		templateType: TemplateTypeAgent,
	})

	require.Len(t, items, 1)
	require.Equal(t, "Echo Agent", items[0].Title)
	require.True(t, items[0].Featured)
	require.True(t, items[0].Recommended)
}

func TestBuildTemplateListItems_EmptyResultIsValid(t *testing.T) {
	t.Parallel()

	// Filter that matches nothing must return an empty slice, not nil, so the
	// JSON envelope serializes as "templates":[] rather than "templates":null.
	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{language: "python", featuredOnly: true, templateType: TemplateTypeAzd})

	require.Empty(t, items)
	require.NotNil(t, items, "must return [] not nil so JSON is templates:[]")
}

func TestInitListJSONShape_EmptyEnvelopeUsesArray(t *testing.T) {
	t.Parallel()

	resp := initListResponse{Templates: buildTemplateListItems(nil, &initListFlags{})}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	// Consumers parse this as an array; null would break them.
	require.Equal(t, `{"templates":[]}`, string(data))
}

func TestInitListJSONShape_StableFieldNames(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{})
	resp := initListResponse{Templates: items}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	// Spot-check field names that form the public contract. If any of these
	// names drift, this test fails loudly so the change is intentional.
	body := string(data)
	for _, want := range []string{
		`"templates":`,
		`"title":`,
		`"languages":`,
		`"type":`,
		`"manifestUrl":`,
		`"featured":`,
		`"recommended":`,
		`"initCommand":`,
	} {
		require.Contains(t, body, want, "missing public field %q", want)
	}

	// Also verify the azd-type item has repoUrl, not manifestUrl.
	require.Contains(t, body, `"repoUrl":"Azure-Samples/azd-agent-starter"`)
}

func TestNormalizeOutputFormat(t *testing.T) {
	t.Parallel()

	// AllowedValues rejects anything other than "json" or "text" at flag
	// parse time, so only those two and the SDK's pre-parse sentinel ever
	// reach normalizeOutputFormat in practice.
	tests := map[string]string{
		"json":    "json",
		"JSON":    "json",
		"text":    "text",
		"default": "text", // SDK pre-substitution sentinel
		"":        "text",
	}
	for in, want := range tests {
		require.Equal(t, want, normalizeOutputFormat(in), "input=%q", in)
	}
}

// TestPrintInitListText_FormatContract asserts the exact format the user
// asked for: each item is a "Sample: <title>" / "Description: <desc>" /
// "Manifest: <url>" block separated by a blank line. No tabular columns,
// no LANG / TYPE / TAGS surface in the text format.
func TestPrintInitListText_FormatContract(t *testing.T) {
	t.Parallel()

	items := []TemplateListItem{
		{
			Title:       "Echo Agent",
			Description: "An agent that echoes input.",
			Type:        TemplateTypeAgent,
			ManifestURL: "https://example.com/echo/agent.yaml",
		},
		{
			Title:       "Full Stack Starter",
			Description: "A full azd template repo.",
			Type:        TemplateTypeAzd,
			RepoURL:     "Azure-Samples/azd-agent-starter",
		},
	}

	var buf strings.Builder
	require.NoError(t, printInitListText(&buf, items))
	got := buf.String()

	// The exact paragraph for the first item, in order.
	require.Contains(t, got, "Sample: Echo Agent\nDescription: An agent that echoes input.\nManifest: https://example.com/echo/agent.yaml\n\n")

	// The azd-type item uses RepoURL in the Manifest line.
	require.Contains(t, got, "Sample: Full Stack Starter\nDescription: A full azd template repo.\nManifest: Azure-Samples/azd-agent-starter\n\n")

	// Removed columns must not appear in the human format.
	require.NotContains(t, got, "LANG")
	require.NotContains(t, got, "TYPE")
	require.NotContains(t, got, "TAGS")
}

func TestPrintInitListText_EmptyShowsMessage(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	require.NoError(t, printInitListText(&buf, nil))
	require.Contains(t, buf.String(), "No templates matched")
}

func TestPrintInitListText_OmitsDescriptionWhenEmpty(t *testing.T) {
	t.Parallel()
	items := []TemplateListItem{
		{Title: "Bare", Type: TemplateTypeAgent, ManifestURL: "https://x/y.yaml"},
	}
	var buf strings.Builder
	require.NoError(t, printInitListText(&buf, items))
	got := buf.String()
	require.Contains(t, got, "Sample: Bare\nManifest: https://x/y.yaml\n\n")
	require.NotContains(t, got, "Description:")
}

// TestBuildTemplateListItems_InitCommandIsReadyToExecute verifies the
// initCommand string is the exact command an AI agent should run, with no
// substitution placeholders or quoting artifacts.
func TestBuildTemplateListItems_InitCommandIsReadyToExecute(t *testing.T) {
	t.Parallel()

	items := buildTemplateListItems(fixtureTemplates(), &initListFlags{})

	for _, it := range items {
		require.NotEmpty(t, it.InitCommand, "InitCommand must always be set for %q", it.Title)
		require.False(t, strings.Contains(it.InitCommand, "<"),
			"InitCommand must not contain placeholders like <url>: %q", it.InitCommand)
		switch it.Type {
		case TemplateTypeAgent:
			require.True(t, strings.HasPrefix(it.InitCommand, "azd ai agent init -m "),
				"agent-type InitCommand must use 'azd ai agent init -m': %q", it.InitCommand)
		case TemplateTypeAzd:
			require.True(t, strings.HasPrefix(it.InitCommand, "azd init -t "),
				"azd-type InitCommand must use 'azd init -t': %q", it.InitCommand)
		}
	}
}
