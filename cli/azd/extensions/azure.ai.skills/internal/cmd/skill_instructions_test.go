// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillInstructions_SchemaAndRoundTrip(t *testing.T) {
	t.Parallel()

	rawSchema, err := os.ReadFile(filepath.Join("..", "..", "schemas", "azure.ai.skill.json"))
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(rawSchema, &schema))
	const resourceURI = "mem://azure.ai.skill.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(resourceURI, schema))
	compiled, err := compiler.Compile(resourceURI)
	require.NoError(t, err)

	tests := []struct {
		name    string
		input   string
		want    skillInstructions
		wantErr bool
	}{
		{name: "inline string", input: `"Review code."`, want: skillInstructions{Value: "Review code."}},
		{
			name: "inline prose ending in extension", input: `"Follow README.md"`,
			want: skillInstructions{Value: "Follow README.md"},
		},
		{
			name: "multiline prose ending in extension", input: `"# Rules\nSee docs/README.md"`,
			want: skillInstructions{Value: "# Rules\nSee docs/README.md"},
		},
		{
			name: "legacy filename", input: `"instructions.md"`,
			want: skillInstructions{Value: "instructions.md", IsFile: true},
		},
		{
			name: "legacy path with spaces", input: `"docs/review instructions.md"`,
			want: skillInstructions{Value: "docs/review instructions.md", IsFile: true},
		},
		{
			name: "legacy windows path with spaces", input: `"docs\\review instructions.md"`,
			want: skillInstructions{Value: `docs\review instructions.md`, IsFile: true},
		},
		{
			name: "legacy spaced directory", input: `"skill files/rules.TXT"`,
			want: skillInstructions{Value: "skill files/rules.TXT", IsFile: true},
		},
		{
			name: "explicit inline filename", input: `{"inline": "README.md"}`,
			want: skillInstructions{Value: "README.md"},
		},
		{
			name: "explicit inline path with spaces", input: `{"inline": "docs/review instructions.md"}`,
			want: skillInstructions{Value: "docs/review instructions.md"},
		},
		{
			name: "explicit spaced filename", input: `{"file": "review instructions.md"}`,
			want: skillInstructions{Value: "review instructions.md", IsFile: true},
		},
		{name: "empty object", input: `{}`, wantErr: true},
		{name: "both sources", input: `{"inline": "Review.", "file": "rules.md"}`, wantErr: true},
		{name: "unknown source", input: `{"path": "rules.md"}`, wantErr: true},
		{name: "extra property", input: `{"inline": "Review.", "extra": "ignored"}`, wantErr: true},
		{name: "empty inline", input: `{"inline": ""}`, wantErr: true},
		{name: "blank inline", input: `{"inline": " \t\n"}`, wantErr: true},
		{name: "empty file", input: `{"file": ""}`, wantErr: true},
		{name: "blank file", input: `{"file": " \t\n"}`, wantErr: true},
		{name: "numeric inline", input: `{"inline": 123}`, wantErr: true},
		{name: "numeric file", input: `{"file": 123}`, wantErr: true},
		{name: "null source", input: `{"inline": null}`, wantErr: true},
		{name: "null extra source", input: `{"inline": null, "file": "rules.md"}`, wantErr: true},
		{name: "number", input: `123`, wantErr: true},
		{name: "array", input: `["rules.md"]`, wantErr: true},
		{name: "null", input: `null`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var input any
			require.NoError(t, json.Unmarshal([]byte(tt.input), &input))
			schemaErr := compiled.Validate(map[string]any{"instructions": input})
			var instructions skillInstructions
			parseErr := json.Unmarshal([]byte(tt.input), &instructions)
			if tt.wantErr {
				require.Error(t, parseErr)
				require.Error(t, schemaErr)
				return
			}
			require.NoError(t, parseErr)
			require.NoError(t, schemaErr)
			assert.Equal(t, tt.want, instructions)

			values, err := skillServiceConfigMap(skillServiceConfig{Instructions: instructions})
			require.NoError(t, err)
			require.NoError(t, compiled.Validate(values))
			encoded, err := json.Marshal(values)
			require.NoError(t, err)
			var decoded skillServiceConfig
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, instructions, decoded.Instructions)
		})
	}
}

func TestSkillInstructions_MarshalPreservesPathLikeInlineText(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"README.md", "docs/review instructions.md", "./skill files/rules.md"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			values, err := skillServiceConfigMap(skillServiceConfig{Instructions: skillInstructions{Value: value}})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"inline": value}, values["instructions"])
		})
	}
}
