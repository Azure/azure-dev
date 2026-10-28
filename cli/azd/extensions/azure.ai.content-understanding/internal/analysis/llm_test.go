// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func frontMatter(t *testing.T, output string) map[string]any {
	t.Helper()
	require.True(t, strings.HasPrefix(output, "---\n"))
	parts := strings.SplitN(strings.TrimPrefix(output, "---\n"), "\n---", 2)
	require.Len(t, parts, 2)
	var decoded map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(parts[0]), &decoded))
	return decoded
}

func TestLLMFieldTypesMetadataAndWarnings(t *testing.T) {
	raw := `{"contents":[{"kind":"document","mimeType":"application/pdf","metadata":{"empty":{},"array":[],"flag":true},
		"fields":{
		"String":{"type":"string","valueString":"true"},
		"Bool":{"type":"boolean","valueBoolean":false},
		"Number":{"type":"number","valueNumber":1.25},
		"Date":{"type":"date","valueDate":"2026-04-12"},
		"Time":{"type":"time","valueTime":"14:30:00"},
		"Object":{"type":"object","valueObject":{"Count":{"type":"integer","valueInteger":0}}},
		"Array":{"type":"array","valueArray":[{"type":"string"},{"type":"string","valueString":"value"}]},
		"Json":{"type":"json","valueJson":{"nested":[null,{},[],true,3]}},
		"Empty":{"type":"object","valueObject":{}},
		"EmptyArray":{"type":"array","valueArray":[{"type":"object","valueObject":{}}]},
		"Missing":{"type":"date"}
		}}],"warnings":[{"code":"Real","message":"Review","target":"fields"},
		{"message":"  LLMStats: hidden"},{"message":"User LLMStats: preserved"},{}]}`
	text, err := RenderLLM(json.RawMessage(raw))
	require.NoError(t, err)
	matter := frontMatter(t, text)
	fields, ok := matter["fields"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "true", fields["String"])
	assert.Equal(t, false, fields["Bool"])
	assert.EqualValues(t, 1.25, fields["Number"])
	assert.Equal(t, "2026-04-12", fields["Date"])
	assert.Equal(t, "14:30:00", fields["Time"])
	assert.Equal(t, map[string]any{"Count": 0}, fields["Object"])
	assert.Equal(t, []any{"value"}, fields["Array"])
	assert.Contains(t, fields, "Json")
	for _, absent := range []string{"Empty", "EmptyArray", "Missing"} {
		assert.NotContains(t, fields, absent)
	}
	assert.Contains(t, matter, "metadata")
	warnings, ok := matter["warnings"].([]any)
	require.True(t, ok)
	require.Len(t, warnings, 2)
	assert.NotContains(t, text, "hidden")
	assert.Less(t, strings.Index(text, "mimeType:"), strings.Index(text, "metadata:"))
	assert.Less(t, strings.Index(text, "metadata:"), strings.Index(text, "fields:"))
	assert.Less(t, strings.Index(text, "fields:"), strings.Index(text, "warnings:"))
}

func TestLLMUnicodePagesSpansAndExistingMarkers(t *testing.T) {
	for _, test := range []struct {
		name, content, body string
		pages               any
	}{
		{"breaks", `{"kind":"document","startPageNumber":2,"endPageNumber":3,"markdown":" A \n<!-- PageBreak -->\nB "}`,
			"<!-- InputPageNumber: 2 -->\n\nA\n\n<!-- InputPageNumber: 3 -->\n\nB", "2-3"},
		{"sparse spans", `{"kind":"document","markdown":"甲乙\n<!-- PageBreak -->\n丙","pages":[
			{"pageNumber":2,"spans":[{"offset":0,"length":2}]},{"pageNumber":5,"spans":[{"offset":22,"length":1}]}]}`,
			"<!-- InputPageNumber: 2 -->\n\n甲乙\n\n<!-- InputPageNumber: 5 -->\n\n丙", "2, 5"},
		{"already marked", `{"kind":"document","startPageNumber":9,"endPageNumber":9,
			"markdown":"<!-- InputPageNumber: 9 -->\n\nText"}`,
			"<!-- InputPageNumber: 9 -->\n\nText", 9},
		{"spans absent", `{"kind":"document","pages":[{"pageNumber":4}],"startPageNumber":4,"markdown":"Text"}`,
			"<!-- InputPageNumber: 4 -->\n\nText", 4},
		{"range compression", `{"kind":"document",
			"pages":[{"pageNumber":5},{"pageNumber":3},{"pageNumber":2}],"markdown":"Text"}`,
			"<!-- InputPageNumber: 1 -->\n\nText", "2-3, 5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			text, err := RenderLLM(json.RawMessage(`{"contents":[` + test.content + `]}`))
			require.NoError(t, err)
			matter := frontMatter(t, text)
			assert.Equal(t, test.pages, matter["pages"])
			assert.True(t, strings.HasSuffix(text, "\n"+test.body), text)
		})
	}
}

func TestLLMClassificationUsesRoutedFieldsAndPageOrder(t *testing.T) {
	raw := `{"contents":[
		{"kind":"document","mimeType":"application/pdf","path":"input1","markdown":"甲乙丙丁",
		 "segments":[
		  {"segmentId":"one","category":"Letter","startPageNumber":1,"endPageNumber":1,"span":{"offset":0,"length":2}},
		  {"segmentId":"two","category":"Invoice","startPageNumber":2,"endPageNumber":2,"span":{"offset":2,"length":2}}]},
		{"kind":"document","path":"input1/two","mimeType":"application/pdf","category":"Invoice",
		 "startPageNumber":2,"endPageNumber":2,"markdown":"Routed invoice",
		 "fields":{"Total":{"type":"integer","valueInteger":42}}}]}`
	text, err := RenderLLM(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(text, "category: Invoice"))
	assert.Contains(t, text, "Total: 42")
	assert.Contains(t, text, "甲乙")
	assert.NotContains(t, text, "丙丁")
	assert.Less(t, strings.Index(text, "category: Letter"), strings.Index(text, "category: Invoice"))
	assert.Equal(t, 1, strings.Count(text, "\n\n*****\n\n"))
}

func TestLLMAudioVisualTimeRangesAndNonDocumentOrdering(t *testing.T) {
	first := `{"kind":"audioVisual","mimeType":"video/mp4","startTimeMs":500,"endTimeMs":61000,"markdown":"First"}`
	second := `{"kind":"audioVisual","mimeType":"video/mp4","startTimeMs":61000,"endTimeMs":122000,"markdown":"Second"}`
	single, err := RenderLLM(json.RawMessage(`{"contents":[` + first + `]}`))
	require.NoError(t, err)
	assert.NotContains(t, single, "timeRange:")
	multi, err := RenderLLM(json.RawMessage(`{"contents":[` + first + `,` + second + `]}`))
	require.NoError(t, err)
	assert.Contains(t, multi, "00:00 \u2013 01:01")
	assert.Contains(t, multi, "01:01 \u2013 02:02")
	assert.Less(t, strings.Index(multi, "First"), strings.Index(multi, "Second"))
	assert.NotContains(t, multi, "InputPageNumber")
}

func TestLLMInvalidEmptyAndUnsupportedResults(t *testing.T) {
	for _, raw := range []string{
		"invalid", "null", "[]", `{}`, `{"contents":[]}`, `{"contents":[{"fields":{"a":null}}]}`,
		`{"contents":[{"fields":{"a":{"type":"future","valueFuture":3}}}]}`,
		`{"contents":[{"fields":{"a":{"type":"object","valueObject":[]}}}]}`,
		`{"contents":[{"fields":{"a":{"type":"array","valueArray":{}}}}]}`,
	} {
		_, err := RenderLLM(json.RawMessage(raw))
		requireCode(t, err, "invalid_llm_result")
	}
}

func TestLLMYAMLOpaqueStringsCannotBreakFrontMatter(t *testing.T) {
	raw := `{"contents":[{"kind":"image","fields":{"Payload":{"type":"string","valueString":"safe\n---\nfields: evil"}},
		"metadata":{"date":"2026-04-12","bool":"true","numeric":"42","json":"{\"x\":1}"}}]}`
	text, err := RenderLLM(json.RawMessage(raw))
	require.NoError(t, err)
	matter := frontMatter(t, text)
	fields, ok := matter["fields"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "safe\n---\nfields: evil", fields["Payload"])
	metadata, ok := matter["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"date": "2026-04-12", "bool": "true", "numeric": "42", "json": `{"x":1}`}, metadata)
}
