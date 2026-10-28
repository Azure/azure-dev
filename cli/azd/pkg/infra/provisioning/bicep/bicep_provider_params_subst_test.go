// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// loadParametersFromFile runs loadParameters against the given parameters file content and environment.
func loadParametersFromFile(
	t *testing.T, paramsFile string, envVars map[string]string,
) (map[string]any, error) {
	t.Helper()

	mockContext := mocks.NewMockContext(t.Context())
	armTemplate := azure.ArmTemplate{
		// nolint: lll
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Parameters: azure.ArmTemplateParameterDefinitions{
			"environmentName": {Type: "string", DefaultValue: "test-env"},
			"location":        {Type: "string", DefaultValue: "westus2"},
			// defaults keep Initialize from prompting for the parameters under test
			"obj":  {Type: "object", DefaultValue: map[string]any{}},
			"arr":  {Type: "array", DefaultValue: []any{}},
			"str":  {Type: "string", DefaultValue: ""},
			"num":  {Type: "int", DefaultValue: 0},
			"flag": {Type: "bool", DefaultValue: false},
		},
		Outputs: azure.ArmTemplateOutputs{},
	}

	infraProvider := createBicepProviderWithEnvAndMode(t, mockContext, armTemplate, envVars, provisioning.ModeDeploy)

	tmpInfraDir := filepath.Join(t.TempDir(), "infra")
	require.NoError(t, os.MkdirAll(tmpInfraDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpInfraDir, "main.parameters.json"), []byte(paramsFile), 0o600))
	infraProvider.path = filepath.Join(tmpInfraDir, "main.bicep")

	compileResult, err := infraProvider.compileBicep(*mockContext.Context)
	require.NoError(t, err)

	result, err := infraProvider.loadParameters(*mockContext.Context, &compileResult.Template)
	if err != nil {
		return nil, err
	}

	values := map[string]any{}
	for name, param := range result.parameters {
		values[name] = param.Value
	}
	return values, nil
}

// paramsFileWith returns a parameters file whose parameters are the given name -> raw JSON "value" entries.
func paramsFileWith(t *testing.T, values map[string]string) string {
	t.Helper()
	params := map[string]any{}
	for name, raw := range values {
		params[name] = map[string]any{"value": json.RawMessage(raw)}
	}
	out, err := json.Marshal(map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     params,
	})
	require.NoError(t, err)
	return string(out)
}

// Scenario 1: "value": "${VAR}" for an object/array parameter resolves the variable as a complete JSON value (#6694).
func TestLoadParameters_WholeValueJSON(t *testing.T) {
	tests := []struct {
		name     string
		param    string
		envValue string
		want     any
	}{
		{"object", "obj", `{"a":1,"b":[true,null,"x"]}`, map[string]any{
			"a": float64(1), "b": []any{true, nil, "x"},
		}},
		{"array of strings", "arr", `["val1","val2"]`, []any{"val1", "val2"}},
		{"nested object and array", "obj", `{"o":{"list":[{"k":"v"},[1,2]]}}`, map[string]any{
			"o": map[string]any{"list": []any{map[string]any{"k": "v"}, []any{float64(1), float64(2)}}},
		}},
		{"quotes, backslashes and newlines stay inside the JSON strings", "obj",
			`{"q":"say \"hi\"","b":"C:\\temp\\new","n":"l1\nl2"}`,
			map[string]any{"q": `say "hi"`, "b": `C:\temp\new`, "n": "l1\nl2"}},
		{"whitespace and newlines around the document", "arr", "[\n  \"a\",\n  \"b\"\n]", []any{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := paramsFileWith(t, map[string]string{tt.param: `"${MY_CONFIG}"`})
			got, err := loadParametersFromFile(t, file, map[string]string{"MY_CONFIG": tt.envValue})
			require.NoError(t, err)
			require.Equal(t, tt.want, got[tt.param])
		})
	}

	t.Run("invalid JSON is reported", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"obj": `"${MY_CONFIG}"`})
		_, err := loadParametersFromFile(t, file, map[string]string{"MY_CONFIG": `{"a": `})
		require.ErrorContains(t, err, "is not valid JSON")
	})

	t.Run("unset variable omits the parameter", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"arr": `"${MY_CONFIG}"`})
		got, err := loadParametersFromFile(t, file, map[string]string{})
		require.NoError(t, err)
		require.NotContains(t, got, "arr")
	})
}

// Scenario 2: placeholders inside strings are replaced with literal text and never change the JSON structure.
func TestLoadParameters_InternalPlaceholdersAreLiteral(t *testing.T) {
	hostile := map[string]string{
		"quotes":       `say "hi" to 'them'`,
		"backslashes":  `C:\temp\new\path\\share`,
		"newlines":     "line1\nline2\r\n\ttabbed",
		"json-in-text": `","injected":"x`,
		"mixed":        "a \"b\" \\ c\nd",
		"json-doc":     `{"a":[1,2]}`,
	}

	for name, text := range hostile {
		t.Run(name, func(t *testing.T) {
			envVars := map[string]string{"FIRST": text, "SECOND": "second"}

			tests := []struct {
				name  string
				param string
				value string
				want  any
			}{
				{"string parameter", "str", `"pre-${FIRST}-post"`, "pre-" + text + "-post"},
				{"whole string parameter", "str", `"${FIRST}"`, text},
				{"object member", "obj", `{"message":"${FIRST}"}`, map[string]any{"message": text}},
				{"array items", "arr", `["${FIRST}","${SECOND}"]`, []any{text, "second"}},
				{"nested objects and arrays", "obj",
					`{"l1":{"l2":[{"m":"x ${FIRST} y"},["${SECOND}",["${FIRST}"]]]},"keep":"plain"}`,
					map[string]any{"l1": map[string]any{"l2": []any{
						map[string]any{"m": "x " + text + " y"},
						[]any{"second", []any{text}},
					}}, "keep": "plain"}},
				{"object key", "obj", `{"${SECOND}":"v"}`, map[string]any{"second": "v"}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					file := paramsFileWith(t, map[string]string{tt.param: tt.value})
					got, err := loadParametersFromFile(t, file, envVars)
					require.NoError(t, err)
					require.Equal(t, tt.want, got[tt.param])
				})
			}
		})
	}

	t.Run("default values are supported", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"obj": `{"a":"${UNSET_VAR=fallback}"}`})
		got, err := loadParametersFromFile(t, file, map[string]string{})
		require.NoError(t, err)
		require.Equal(t, map[string]any{"a": "fallback"}, got["obj"])
	})

	t.Run("string parameter mapped to an unset variable is omitted", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"str": `"${NOT_SET}"`})
		got, err := loadParametersFromFile(t, file, map[string]string{})
		require.NoError(t, err)
		require.NotContains(t, got, "str")
	})
}

// Scenario 3: values authored directly in the parameters file keep their structure and types.
func TestLoadParameters_AuthoredValuesKeepTypes(t *testing.T) {
	file := paramsFileWith(t, map[string]string{
		"obj":  `{"s":"text","n":3,"f":1.5,"b":false,"nil":null,"arr":[1,"two",{"three":3}],"empty":{}}`,
		"arr":  `[1,"two",true,null,[3],{"four":4}]`,
		"str":  `"a \"quoted\" \\ value\n"`,
		"num":  `42`,
		"flag": `true`,
	})
	got, err := loadParametersFromFile(t, file, map[string]string{"UNUSED": "x"})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"s": "text", "n": float64(3), "f": 1.5, "b": false, "nil": nil,
		"arr": []any{float64(1), "two", map[string]any{"three": float64(3)}}, "empty": map[string]any{},
	}, got["obj"])
	require.Equal(t,
		[]any{float64(1), "two", true, nil, []any{float64(3)}, map[string]any{"four": float64(4)}}, got["arr"])
	require.Equal(t, "a \"quoted\" \\ value\n", got["str"])
	require.Equal(t, float64(42), got["num"])
	require.Equal(t, true, got["flag"])
}

// Replacement text containing JSON syntax or placeholders must be inserted once and never re-evaluated.
func TestLoadParameters_ReplacementIsNotReinterpreted(t *testing.T) {
	file := paramsFileWith(t, map[string]string{
		"obj": `{"a":"${A}","b":"${B}"}`,
	})
	got, err := loadParametersFromFile(t, file, map[string]string{
		"A": `{"nested":"object"}`,
		"B": "${A}",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": `{"nested":"object"}`, "b": "${A}"}, got["obj"])
}

// The pipeline escapes values (see escapeValuesForPipeline in pkg/pipeline) before storing them in the CI system. azd
// reads such a value as a plain environment variable and, because the replacement text is literal, does not decode
// it again.
func TestLoadParameters_PipelineEscapedValueIsNotEscapedAgain(t *testing.T) {
	// Result of escapeValuesForPipeline for the value ["api://guid"].
	const pipelineEscaped = `[\"api://guid\"]`

	t.Run("escaped value is used as is", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"str": `"${AUDIENCES}"`})
		got, err := loadParametersFromFile(t, file, map[string]string{"AUDIENCES": pipelineEscaped})
		require.NoError(t, err)
		require.Equal(t, pipelineEscaped, got["str"])
	})

	t.Run("unescaped JSON text in a string parameter stays a string", func(t *testing.T) {
		file := paramsFileWith(t, map[string]string{"str": `"${AUDIENCES}"`})
		got, err := loadParametersFromFile(t, file, map[string]string{"AUDIENCES": `["api://guid"]`})
		require.NoError(t, err)
		require.Equal(t, `["api://guid"]`, got["str"])
	})
}
