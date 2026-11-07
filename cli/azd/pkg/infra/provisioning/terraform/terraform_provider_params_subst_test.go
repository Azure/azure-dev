// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

// substituteTemplate writes template to disk, runs createInputParametersFile for it and returns the resulting file.
func substituteTemplate(t *testing.T, template string, envVars map[string]string) (string, error) {
	t.Helper()

	env := environment.NewWithValues("test-env", envVars)
	provider := &TerraformProvider{env: env, curPrincipal: &mockCurrentPrincipal{}}

	dir := t.TempDir()
	templatePath := filepath.Join(dir, "main.tfvars.json")
	outputPath := filepath.Join(dir, "out", "main.tfvars.json")
	require.NoError(t, os.WriteFile(templatePath, []byte(template), 0o600))

	if err := provider.createInputParametersFile(t.Context(), templatePath, outputPath); err != nil {
		return "", err
	}

	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	return string(content), nil
}

func substituteTemplateToMap(t *testing.T, template string, envVars map[string]string) map[string]any {
	t.Helper()

	content, err := substituteTemplate(t, template, envVars)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &parsed), "output must be valid JSON: %s", content)
	return parsed
}

func TestCreateInputParametersFile_ValuesAreLiteral(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"quotes", `say "hello"`},
		{"backslashes", `C:\path\to\file`},
		{"json array text", `["api://guid","b"]`},
		{"json object text", `{"a":1}`},
		{"newlines", "line1\nline2\r\nline3"},
		{"escape sequences", `a\nb\"c`},
		{"html characters", `<a href="x">&</a>`},
		{"placeholder text", `${OTHER}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteTemplateToMap(t,
				`{"plain": "${MY_VALUE}", "mixed": "prefix-${MY_VALUE}-suffix", "list": ["${MY_VALUE}"], `+
					`"nested": {"deep": {"value": "${MY_VALUE}"}}}`,
				map[string]string{"MY_VALUE": tt.value, "OTHER": "must-not-be-used"})

			require.Equal(t, map[string]any{
				"plain":  tt.value,
				"mixed":  "prefix-" + tt.value + "-suffix",
				"list":   []any{tt.value},
				"nested": map[string]any{"deep": map[string]any{"value": tt.value}},
			}, got)
		})
	}
}

// A value as stored by azd pipeline config is the same text as the local value, so CI and local runs match.
func TestCreateInputParametersFile_PipelineValueMatchesLocalRun(t *testing.T) {
	const value = `say "hi" C:\path ["api://guid","b"]` + "\nsecond line"
	template := `{"value": "${MY_VALUE}"}`

	local := substituteTemplateToMap(t, template, map[string]string{"MY_VALUE": value})
	ci := substituteTemplateToMap(t, template, map[string]string{"MY_VALUE": value})

	require.Equal(t, value, local["value"])
	require.Equal(t, local, ci)
}

func TestCreateInputParametersFile_TypesArePreserved(t *testing.T) {
	got := substituteTemplateToMap(t,
		`{"count": 3, "big": 12345678901234567890, "ratio": 1.5, "flag": true, "nothing": null, `+
			`"tags": {"env": "${ENV_NAME}"}, "ports": [80, 443]}`,
		map[string]string{"ENV_NAME": "dev"})

	require.Equal(t, map[string]any{
		"count":   float64(3),
		"big":     float64(12345678901234567890),
		"ratio":   1.5,
		"flag":    true,
		"nothing": nil,
		"tags":    map[string]any{"env": "dev"},
		"ports":   []any{float64(80), float64(443)},
	}, got)
}

func TestCreateInputParametersFile_ObjectKeysAreSubstituted(t *testing.T) {
	got := substituteTemplateToMap(t,
		`{"tags": {"${KEY}": "v"}}`, map[string]string{"KEY": `we"ird\key`})
	require.Equal(t, map[string]any{"tags": map[string]any{`we"ird\key`: "v"}}, got)
}

func TestCreateInputParametersFile_UnsetVariablesAndDefaults(t *testing.T) {
	got := substituteTemplateToMap(t,
		`{"unset": "${NOT_SET}", "withDefault": "${NOT_SET=fallback}", "set": "${SET_VAR=fallback}", `+
			`"principal": "${AZURE_PRINCIPAL_ID}"}`,
		map[string]string{"SET_VAR": "actual"})

	require.Equal(t, map[string]any{
		"unset":       "",
		"withDefault": "fallback",
		"set":         "actual",
		"principal":   "11111111-1111-1111-1111-111111111111",
	}, got)
}

// A template that is not valid JSON before substitution is substituted as plain text, as it always was.
func TestCreateInputParametersFile_NonJSONTemplateFallsBackToText(t *testing.T) {
	content, err := substituteTemplate(t, `{"count": ${COUNT}, "name": "${NAME}"}`,
		map[string]string{"COUNT": "5", "NAME": "app"})
	require.NoError(t, err)
	require.Equal(t, `{"count": 5, "name": "app"}`, content)
}

// In the text fallback, values that land inside a JSON string literal are JSON-escaped so the file stays valid, while
// unquoted placeholders are substituted as written.
func TestCreateInputParametersFile_NonJSONTemplateEscapesStringValues(t *testing.T) {
	const name = `say "hi" C:\path` + "\nnext"
	got := substituteTemplateToMap(t,
		`{"count": ${COUNT}, "name": "${NAME}", "mixed": "a-${NAME}-b", "esc": "x\"${NAME}", "list": ["${NAME}"], `+
			`"fallback": "${MISSING=de\"fault}"}`,
		map[string]string{"COUNT": "5", "NAME": name})

	require.Equal(t, map[string]any{
		"count":    float64(5),
		"name":     name,
		"mixed":    "a-" + name + "-b",
		"esc":      `x"` + name,
		"list":     []any{name},
		"fallback": `de"fault`,
	}, got)
}

func TestCreateInputParametersFile_NonJSONTemplateEnvsubstOperators(t *testing.T) {
	got := substituteTemplateToMap(t,
		`{"count": ${UNSET_TEST_COUNT=5}, "name": ${UNSET_TEST_NAME="app"}, "part": "${QUOTED:1:3}"}`,
		map[string]string{"QUOTED": `"abc`})

	require.Equal(t, map[string]any{
		"count": float64(5),
		"name":  "app",
		"part":  "abc",
	}, got)
}
