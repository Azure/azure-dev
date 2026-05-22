// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBicepOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "empty input → nil",
			in:   "",
			want: nil,
		},
		{
			name: "single output, common shape",
			in: `param location string = resourceGroup().location

output FOUNDRY_PROJECT_ENDPOINT string = aiProject.outputs.endpoint
`,
			want: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "multiple outputs in sorted order regardless of source order",
			in: `output ZED string = ''
output ALPHA string = ''
output MIDDLE string = ''
`,
			want: []string{"ALPHA", "MIDDLE", "ZED"},
		},
		{
			name: "duplicate output names deduplicated",
			in: `output FOO string = ''
output FOO string = ''
`,
			want: []string{"FOO"},
		},
		{
			name: "typed array output",
			in:   `output FOO string[] = []`,
			want: []string{"FOO"},
		},
		{
			name: "optional/nullable type output",
			in:   `output FOO string? = null`,
			want: []string{"FOO"},
		},
		{
			name: "dotted/qualified type output",
			in:   `output FOO Microsoft.Storage = bar`,
			want: []string{"FOO"},
		},
		{
			name: "literal-union type output",
			in:   `output FOO 'gpt-4o' | 'gpt-4.1' = 'gpt-4o'`,
			want: []string{"FOO"},
		},
		{
			name: "inferred-type output (no type token between name and =) rejected",
			in:   `output FOO = 'no-type'`,
			want: nil,
		},
		{
			name: "ternary right-hand side accepted",
			in:   `output AZURE_AI_PROJECT_ID string = useExistingAiProject ? existingAiProject.outputs.projectId : aiProject.outputs.projectId`,
			want: []string{"AZURE_AI_PROJECT_ID"},
		},
		{
			name: "indented output (inside a conditional block) accepted",
			in: `if (condition) {
  output INDENTED_OUTPUT string = 'value'
}
`,
			want: []string{"INDENTED_OUTPUT"},
		},
		{
			name: "underscore-prefixed name accepted",
			in:   `output _PRIVATE_THING string = ''`,
			want: []string{"_PRIVATE_THING"},
		},
		{
			name: "non-AZURE prefix output is captured (spec compliance)",
			in: `output APPLICATIONINSIGHTS_CONNECTION_STRING string = ''
output BING_GROUNDING_CONNECTION_ID string = ''
output TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT string = ''
`,
			want: []string{
				"APPLICATIONINSIGHTS_CONNECTION_STRING",
				"BING_GROUNDING_CONNECTION_ID",
				"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
			},
		},
		{
			name: "single-line comment masks output declaration",
			in: `// output COMMENTED_OUT string = 'foo'
output REAL_ONE string = ''
`,
			want: []string{"REAL_ONE"},
		},
		{
			name: "trailing single-line comment after output declaration captured",
			in:   `output FOO string = '' // this is a comment`,
			want: []string{"FOO"},
		},
		{
			name: "block comment spanning multiple lines suppresses outputs inside",
			in: `/* output HIDDEN_A string = ''
output HIDDEN_B string = ''
*/
output VISIBLE string = ''
`,
			want: []string{"VISIBLE"},
		},
		{
			name: "block comment opening and closing on same line does not suppress later output on same line",
			in:   `/* hidden */ output SURFACE string = ''`,
			want: []string{"SURFACE"},
		},
		{
			name: "@description decorator on previous line does not interfere",
			in: `@description('Project endpoint URL')
output FOUNDRY_PROJECT_ENDPOINT string = 'value'
`,
			want: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		},
		{
			name: "param / var / resource keywords are ignored",
			in: `param p string = ''
var v = 'x'
resource r 'Microsoft.Foo/bar@2024-01-01' = {}
output ACTUAL_OUTPUT string = ''
`,
			want: []string{"ACTUAL_OUTPUT"},
		},
		{
			name: "output keyword must be followed by name and type — bare 'output' line ignored",
			in: `output
output ONLY_REAL_ONE string = ''
`,
			want: []string{"ONLY_REAL_ONE"},
		},
		{
			name: "no outputs → nil",
			in: `param x string = ''
var y = x
`,
			want: nil,
		},
		{
			name: "names starting with a digit are not valid Bicep identifiers and are not captured",
			in:   `output 9INVALID string = ''`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseBicepOutputs(strings.NewReader(tc.in))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDiscoverBicepOutputs_MissingFileOrEmptyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		projectPath string
	}{
		{name: "empty projectPath returns nil", projectPath: ""},
		{name: "non-existent projectPath returns nil", projectPath: filepath.Join(t.TempDir(), "does-not-exist")},
		{
			name:        "projectPath without infra/main.bicep returns nil",
			projectPath: t.TempDir(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, discoverBicepOutputs(tc.projectPath))
		})
	}
}

func TestDiscoverBicepOutputs_RealFile(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0o750))
	contents := `// Auto-generated header

param location string = resourceGroup().location

output FOUNDRY_PROJECT_ENDPOINT string = aiProject.outputs.endpoint
output TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT string = toolbox.outputs.mcpEndpoint

/* output COMMENTED_OUT string = '' */
output APPLICATIONINSIGHTS_CONNECTION_STRING string = appInsights.outputs.connectionString
`
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte(contents),
		0o600,
	))

	got := discoverBicepOutputs(projectRoot)
	want := []string{
		"APPLICATIONINSIGHTS_CONNECTION_STRING",
		"FOUNDRY_PROJECT_ENDPOINT",
		"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
	}
	assert.Equal(t, want, got)
}

func TestStripBicepComments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		line        string
		inBlock     bool
		wantOut     string
		wantInBlock bool
	}{
		{
			name:    "plain line passes through unchanged",
			line:    "output FOO string = ''",
			wantOut: "output FOO string = ''",
		},
		{
			name:    "line comment trims trailing text",
			line:    "output FOO string = '' // explanation",
			wantOut: "output FOO string = '' ",
		},
		{
			name:    "single-line block comment removed inline",
			line:    "output /* note */ FOO string = ''",
			wantOut: "output  FOO string = ''",
		},
		{
			name:        "block comment opened but not closed flags inBlock",
			line:        "output /* eaten",
			wantOut:     "output ",
			wantInBlock: true,
		},
		{
			name:        "continuation of block comment with no closer keeps state",
			line:        "still inside the comment",
			inBlock:     true,
			wantOut:     "",
			wantInBlock: true,
		},
		{
			name:        "continuation of block comment with closer clears state",
			line:        "still inside */ output VISIBLE string = ''",
			inBlock:     true,
			wantOut:     " output VISIBLE string = ''",
			wantInBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, gotInBlock := stripBicepComments(tc.line, tc.inBlock)
			assert.Equal(t, tc.wantOut, got)
			assert.Equal(t, tc.wantInBlock, gotInBlock)
		})
	}
}
