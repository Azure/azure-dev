// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"encoding/json"
	"fmt"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/require"
)

func TestSourceLocations(t *testing.T) {
	const body = "{\r\n  \"private.é\": {\"type\": \"currency\"},\r\n" +
		"  \"private\": {\"é\": {\"type\": \"extarct\"}},\r\n" +
		"  \"array\": [{\"type\": \"bad\"}],\r\n" +
		"  \"escaped\\\"name\": {\"type\": 123456789012345678901234567890}\r\n}"
	locations, err := SourceLocations([]byte(body))
	require.NoError(t, err)
	for _, test := range []struct {
		path, want string
	}{
		{"$", "Line 1, column 1"},
		{"private.é.type", "Line 2, column 17; Line 3, column 21"},
		{"private.é.items", "Line 2, column 3; Line 3, column 15"},
		{"array.0.type", "Line 4, column 14"},
		{`escaped"name.type`, "Line 5, column 21"},
		{"baseAnalyzerId", "baseAnalyzerId"},
	} {
		t.Run(test.path, func(t *testing.T) {
			label := (Finding{Path: test.path}).DiagnosticLocation(locations)
			require.Equal(t, test.want, label)
			require.NotContains(t, label, "private")
			require.NotContains(t, label, "currency")
			require.NotContains(t, label, "extarct")
		})
	}
}

func TestSourceLocationsInvalidJSON(t *testing.T) {
	_, err := SourceLocations([]byte(`{"private":`))
	require.Error(t, err)
}

func TestAuthoringDiagnosticLocationUsesSelectedAlias(t *testing.T) {
	for _, primary := range []string{"{}", "[]", "null", "false", "0", `""`} {
		t.Run(primary, func(t *testing.T) {
			body := fmt.Appendf(nil, "{\n  \"fieldSchema\": %s,\n  \"field_schema\": {\n"+
				"    \"fields\": {\n      \"private\": {\n        \"type\": \"currency\"\n      }\n    }\n  }\n}", primary)
			result, err := Validate(body, cuclient.DefaultAPIVersion, false)
			require.NoError(t, err)
			locations, err := SourceLocations(body)
			require.NoError(t, err)
			require.Len(t, result.Errors, 1)
			finding := result.Errors[0]
			require.Equal(t, "fieldSchema.fields.private.type", finding.Path)
			require.Equal(t, "Line 6, column 9", finding.DiagnosticLocation(locations))
			encoded, err := json.Marshal(finding)
			require.NoError(t, err)
			require.JSONEq(t, `{"path":"fieldSchema.fields.private.type",
				"msg":"Specify a field type: array, boolean, date, integer, json, number, object, string, time."}`,
				string(encoded))
		})
	}
}

func TestDiagnosticLocationKeepsSpecAndAuthoringSourcesSeparate(t *testing.T) {
	const body = "{\n  \"fieldSchema\": false,\n  \"field_schema\": []\n}"
	result, err := Validate([]byte(body), cuclient.DefaultAPIVersion, true)
	require.NoError(t, err)
	locations, err := SourceLocations([]byte(body))
	require.NoError(t, err)
	authoring, spec := false, false
	for _, finding := range result.Errors {
		if finding.Path != "fieldSchema" {
			continue
		}
		if finding.Message == "The field schema must be an object." {
			authoring = true
			require.Equal(t, "Line 3, column 3", finding.DiagnosticLocation(locations))
		} else {
			spec = true
			require.Contains(t, finding.Message, "service contract")
			require.Equal(t, "Line 2, column 3", finding.DiagnosticLocation(locations))
		}
	}
	require.True(t, authoring, "expected the authoring finding for the selected alias")
	require.True(t, spec, "expected the literal service-contract finding for the primary")
}

func TestAuthoringDiagnosticLocationPreservesAliasPrecedence(t *testing.T) {
	for _, test := range []struct {
		primary, secondary, value, location string
	}{
		{"analyzerId", "analyzer_id", `""`, "Line 3, column 3"},
		{"baseAnalyzerId", "base_analyzer_id", `""`, "Line 3, column 3"},
		{"analyzerId", "analyzer_id", `"bad/id"`, "Line 2, column 3"},
		{"baseAnalyzerId", "base_analyzer_id", `"bad/id"`, "Line 2, column 3"},
	} {
		t.Run(test.primary+"/"+test.value, func(t *testing.T) {
			body := fmt.Appendf(nil, "{\n  %q: %s,\n  %q: 123\n}", test.primary, test.value, test.secondary)
			result, err := Validate(body, cuclient.DefaultAPIVersion, false)
			require.NoError(t, err)
			locations, err := SourceLocations(body)
			require.NoError(t, err)
			require.Len(t, result.Errors, 1)
			require.Equal(t, test.primary, result.Errors[0].Path)
			require.Equal(t, test.location, result.Errors[0].DiagnosticLocation(locations))
		})
	}
}

func TestAuthoringWarningLocationUsesSelectedAlias(t *testing.T) {
	const body = "{\n  \"fieldSchema\": {},\n  \"field_schema\": {\n    \"description\": \"A complete description.\"\n  }\n}"
	result, err := Validate([]byte(body), cuclient.DefaultAPIVersion, false)
	require.NoError(t, err)
	locations, err := SourceLocations([]byte(body))
	require.NoError(t, err)
	found := false
	for _, finding := range result.Warnings {
		if finding.Path == "fieldSchema.fields" {
			found = true
			require.Equal(t, "Line 3, column 3", finding.DiagnosticLocation(locations))
		}
	}
	require.True(t, found, "expected an authoring warning for missing fields")
}
