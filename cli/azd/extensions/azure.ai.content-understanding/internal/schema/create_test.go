// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomAnalyzerID(t *testing.T) {
	for _, name := range []string{"invoice_v1", "A", "123", strings.Repeat("a", 64)} {
		assert.NoError(t, ValidateCustomAnalyzerID(name))
	}
	for _, name := range []string{"", "invoice-v1", "invoice.v1", "a/b", "你好", strings.Repeat("a", 65)} {
		assert.Error(t, ValidateCustomAnalyzerID(name))
	}
}

func TestPrepareDefinition(t *testing.T) {
	for _, source := range []string{
		`{}`,
		`{"analyzerId":"invoice_v1","baseAnalyzerId":"prebuilt-document","apiVersion":"2025-11-01"}`,
		`{"analyzer_id":"invoice_v1","base_analyzer_id":"prebuilt-document","field_schema":{"fields":{}}}`,
		`{"fieldSchema":{},"field_schema":{"definitions":{"Value":{"type":"number"}}}}`,
		`{"config":{"enableSegment":true,"segmentPerPage":false,"estimateFieldSourceAndConfidence":null}}`,
		`{"models":{},"config":null,"fieldSchema":null,"apiVersion":null}`,
		`{"config":{"contentCategories":{"invoice":{"description":"Invoice","analyzerId":"prebuilt-invoice"}}}}`,
		`{"fieldSchema":{"fields":{
			"String":{"type":"string","method":"extract","estimateSourceAndConfidence":true},
			"Date":{"type":"date","description":null},
			"Time":{"type":"time","method":"generate"},
			"Integer":{"type":"integer"},"Boolean":{"type":"boolean"},"Json":{"type":"json"},
			"Choice":{"type":"string","method":"classify","enum":["A","B"]},
			"Array":{"type":"array","items":{"type":"object","properties":{"Nested":{"type":"number"}}}}
		},"definitions":{"Money":{"type":"number"}}}}`,
		`{"future":{"number":9007199254740993123456789},"description":"保留"} `,
	} {
		t.Run(source, func(t *testing.T) {
			body, pinned, err := PrepareDefinition([]byte(source))
			require.NoError(t, err)
			assert.JSONEq(t, source, string(body))
			if strings.Contains(source, `"apiVersion":"2025-11-01"`) {
				assert.Equal(t, "2025-11-01", pinned)
			}
		})
	}
}

func TestInvalidDefinition(t *testing.T) {
	for _, tt := range []struct{ source, path string }{
		{`not-json`, "$"}, {`[]`, "$"}, {`null`, "$"}, {`{"a":true} private-value`, "$"},
		{`{"analyzerId":5}`, "analyzerId"}, {`{"analyzerId":"prebuilt-bad"}`, "analyzerId"},
		{`{"baseAnalyzerId":"private-value/bad"}`, "baseAnalyzerId"}, {`{"baseAnalyzerId":5}`, "baseAnalyzerId"},
		{`{"apiVersion":5}`, "apiVersion"}, {`{"apiVersion":""}`, "apiVersion"},
		{`{"models":[]}`, "models"}, {`{"config":[]}`, "config"},
		{`{"config":{"enableSegment":"private-value"}}`, "config.enableSegment"},
		{`{"config":{"segmentPerPage":5}}`, "config.segmentPerPage"},
		{`{"config":{"estimateFieldSourceAndConfidence":5}}`, "config.estimateFieldSourceAndConfidence"},
		{`{"config":{"contentCategories":{}}}`, "config.contentCategories"},
		{`{"config":{"contentCategories":[]}}`, "config.contentCategories"},
		{`{"config":{"contentCategories":{"Invoice":false}}}`, "config.contentCategories.Invoice"},
		{`{"config":{"contentCategories":{"Invoice":{"description":5}}}}`, "config.contentCategories.Invoice.description"},
		{`{"config":{"contentCategories":{"Invoice":{"analyzerId":false}}}}`, "config.contentCategories.Invoice.analyzerId"},
		{`{"fieldSchema":[]}`, "fieldSchema"}, {`{"fieldSchema":{"fields":[]}}`, "fieldSchema.fields"},
		{`{"fieldSchema":{"definitions":[]}}`, "fieldSchema.definitions"},
		{`{"fieldSchema":{"fields":{"Total":5}}}`, "fieldSchema.fields.Total"},
		{`{"fieldSchema":{"fields":{"Total":{"$ref":null}}}}`, "fieldSchema.fields.Total.$ref"},
		{`{"fieldSchema":{"fields":{"Total":{}}}}`, "fieldSchema.fields.Total.type"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"private-value"}}}}`, "fieldSchema.fields.Total.type"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"number","description":5}}}}`, "fieldSchema.fields.Total.description"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"string","method":5}}}}`, "fieldSchema.fields.Total.method"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"string","method":"classify"}}}}`, "fieldSchema.fields.Total.enum"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"string","method":"classify","enum":null}}}}`,
			"fieldSchema.fields.Total.enum"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"string","method":"classify","enum":["A","A"]}}}}`,
			"fieldSchema.fields.Total.enum"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"string","method":"classify","enum":[1,"1"]}}}}`,
			"fieldSchema.fields.Total.enum"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"array"}}}}`, "fieldSchema.fields.Total.items"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"array","items":{}}}}}`, "fieldSchema.fields.Total.items.type"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"object","properties":{}}}}}`, "fieldSchema.fields.Total.properties"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"object","properties":{"Inner":false}}}}}`,
			"fieldSchema.fields.Total.properties.Inner"},
		{`{"fieldSchema":{"fields":{"Total":{"type":"number","estimateSourceAndConfidence":5}}}}`,
			"fieldSchema.fields.Total.estimateSourceAndConfidence"},
	} {
		t.Run(tt.path+"/"+tt.source, func(t *testing.T) {
			body, _, err := PrepareDefinition([]byte(tt.source))
			require.Error(t, err)
			assert.Nil(t, body)
			domain, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok)
			assert.Equal(t, cuclient.ErrorValidation, domain.Kind)
			assert.Contains(t, domain.Message, "at "+tt.path+":")
			assert.NotContains(t, domain.Message, "private-value")
		})
	}
}

func TestSchemaUTF8AndDuplicateProperties(t *testing.T) {
	_, _, err := PrepareDefinition([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})
	assert.Error(t, err)
	body, _, err := PrepareDefinition([]byte(`{"models":[],"models":{},"future":9007199254740993}`))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	assert.Equal(t, "9007199254740993", string(fields["future"]))
	assert.Equal(t, "{}", string(fields["models"]))
}
