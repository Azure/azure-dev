// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidationMatchesPythonParityVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/validation.json")
	require.NoError(t, err)
	var fixture struct {
		Source string
		Cases  []struct {
			Name        string
			Body        json.RawMessage
			InvalidJSON bool `json:"invalidJson"`
			Errors      []string
			Warnings    []string
		}
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	assert.Contains(t, fixture.Source, "25d39b30116a840491b7b6f393d6db792b38d4a4")
	require.Len(t, fixture.Cases, 40)
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			data := []byte(item.Body)
			if item.InvalidJSON {
				var text string
				require.NoError(t, json.Unmarshal(data, &text))
				data = []byte(text)
			}
			for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
				// The pinned-version mismatch vector is specific to GA.
				if item.Name == "apiVersion" && version != cuclient.DefaultAPIVersion {
					continue
				}
				result, err := Validate(data, version, false)
				require.NoError(t, err)
				assert.Equal(t, len(item.Errors) == 0, result.OK)
				paths := func(findings []Finding) []string {
					result := []string{}
					for _, finding := range findings {
						result = append(result, finding.Path)
						assert.NotEmpty(t, finding.Message)
					}
					slices.Sort(result)
					return result
				}
				assert.Equal(t, item.Errors, paths(result.Errors), version)
				assert.Equal(t, item.Warnings, paths(result.Warnings), version)
			}
		})
	}
}

func TestFuturePreviewValidationContract(t *testing.T) {
	body := []byte(`{"baseAnalyzerId":"prebuilt-document","apiVersion":"2027-01-01-preview"}`)
	result, err := Validate(body, "2027-01-01-preview", false)
	require.NoError(t, err)
	assert.True(t, result.OK)
	_, err = Validate(body, "2027-01-01-preview", true)
	var domain *cuclient.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, "unsupported_validation_contract", domain.Code)
	assert.Contains(t, domain.Suggestion, "Omit --spec")
}

func TestValidationErrorsAreAggregatedAndDoNotEchoValues(t *testing.T) {
	result, err := Validate([]byte(`{
		"analyzerId":"private-invalid-id","models":[],
		"fieldSchema":{"fields":{
			"A":{"type":"private","method":"private","description":2},
			"B":{"type":"object","properties":{}},
			"C":{"type":"array","items":null}
		}}
	}`), "", true)
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.GreaterOrEqual(t, len(result.Errors), 7)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private")
	for _, path := range []string{"analyzerId", "models", "fieldSchema.fields.A.type", "fieldSchema.fields.B.properties"} {
		assert.Contains(t, string(encoded), path)
	}
	_, err = Validate([]byte(`{}`), "private-version", true)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private")
	result, err = Validate([]byte{0xff}, "", false)
	require.NoError(t, err)
	assert.False(t, result.OK)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "$", result.Errors[0].Path)
}

func TestSpecContractCreateViewAndConstraints(t *testing.T) {
	for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
		t.Run(version, func(t *testing.T) {
			body := []byte(`{"baseAnalyzerId":"prebuilt-document","description":"A useful analyzer."}`)
			result, err := Validate(body, version, true)
			require.NoError(t, err)
			assert.Empty(t, result.Errors, "server-generated required properties must not block authoring")
			for _, body := range []string{
				`{"description":5}`, `{"models":{"completion":42}}`,
				`{"config":{"returnDetails":"true"}}`,
				`{"fieldSchema":[]}`,
				`{"fieldSchema":{"fields":{"Total":{"type":"string","enum":[1,2]}}}}`,
			} {
				result, err := Validate([]byte(body), version, true)
				require.NoError(t, err)
				assert.False(t, result.OK, body)
				assert.NotEmpty(t, result.Errors, body)
				assert.Contains(t, result.Errors[len(result.Errors)-1].Message, "service contract")
			}
		})
	}
}

func TestAuthoringAliasesPreserveSharedEmptyValueSemantics(t *testing.T) {
	for _, empty := range []string{"null", "false", "0", `""`, "{}", "[]"} {
		body := []byte(`{"analyzerId":` + empty + `,"baseAnalyzerId":` + empty + `,"fieldSchema":` + empty + `}`)
		result, err := Validate(body, "", false)
		require.NoError(t, err)
		assert.True(t, result.OK)
		assert.Len(t, result.Warnings, 2)
	}
	result, err := Validate([]byte(`{
		"analyzerId":"","analyzer_id":"good_id",
		"baseAnalyzerId":"","base_analyzer_id":"prebuilt-document",
		"fieldSchema":{},"field_schema":{"fields":{"Amount":{"type":"number"}}}
	}`), "", false)
	require.NoError(t, err)
	assert.True(t, result.OK)
	require.Len(t, result.Warnings, 2)
	assert.Equal(t, "models.completion", result.Warnings[0].Path)
	assert.Equal(t, "fieldSchema.fields.Amount.description", result.Warnings[1].Path)
}

func TestSpecContractFailureNeverSilentlySkipsOrLoadsRemoteReferences(t *testing.T) {
	for _, document := range []any{
		map[string]any{"$ref": "https://private.invalid/schema"},
		map[string]any{"type": "private-invalid-type"},
		map[string]any{"$ref": "#/definitions/missing"},
	} {
		_, err := validateContract([]byte(`{}`), document, cuclient.DefaultAPIVersion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not be loaded or compiled")
		assert.NotContains(t, err.Error(), "private")
	}
	_, err := validateSpec([]byte(`{}`), "missing")
	require.Error(t, err)
	_, err = validateContract([]byte(`{`), map[string]any{"type": "object"}, cuclient.DefaultAPIVersion)
	require.Error(t, err)
	_, err = (offlineContractLoader{}).Load("https://private.invalid")
	require.Error(t, err)
}

func TestBundledContractsHavePinnedProvenanceAndNoExternalReferences(t *testing.T) {
	for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
		raw, err := contracts.ReadFile("specs/" + version + ".json")
		require.NoError(t, err)
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		require.NoError(t, err)
		document, ok := value.(map[string]any)
		require.True(t, ok)
		assert.Contains(t, document["x-cu-source"], "25d39b30116a840491b7b6f393d6db792b38d4a4")
		assert.Contains(t, document["x-cu-source"], version)
		assert.Regexp(t, "^[0-9a-f]{64}$", document["x-cu-source-sha256"])
		assert.Contains(t, document["x-cu-license"], "MIT")
		var inspect func(any)
		inspect = func(node any) {
			switch node := node.(type) {
			case map[string]any:
				if ref, ok := node["$ref"].(string); ok {
					assert.Contains(t, ref, "#/definitions/")
				}
				for _, child := range node {
					inspect(child)
				}
			case []any:
				for _, child := range node {
					inspect(child)
				}
			}
		}
		inspect(document)
	}
}
