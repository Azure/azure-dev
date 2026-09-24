// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputItemJSONRetainsUnknownResultAndSampleFields(t *testing.T) {
	const response = `{
		"id":"service-item-id","run_id":"run_1","status":"completed",
		"datasource_item":{"query":"synthetic input"},
		"datasource_item_id":9007199254740993,
		"results":[{"name":"quality","metric":"quality","score":0.5,"passed":false,
			"threshold":0.7,"properties":{"opaque_result":{"value":4,"details":null}},
			"sample":{"model":"judge","usage":{"total_tokens":123},
				"output":[{"role":"assistant","content":"synthetic response"}],"error":null}}],
		"sample":{"usage":null,"latency_ms":42},
		"future_field":{"kept":[false,null,9007199254740993]}
	}`
	for _, list := range []bool{false, true} {
		name, body := "detail", response
		if list {
			name, body = "list", `{"data":[`+response+`],"has_more":false}`
		}
		t.Run(name, func(t *testing.T) {
			client, _ := recorder(t, http.StatusOK, body)
			var item OutputItem
			if list {
				page, err := client.ListOutputItemsPage(t.Context(), "eval_1", "run_1", 10, "")
				require.NoError(t, err)
				require.Len(t, page.Data, 1)
				item = page.Data[0]
			} else {
				got, err := client.GetOutputItem(t.Context(), "eval_1", "run_1", "1")
				require.NoError(t, err)
				require.NotNil(t, got)
				item = *got
			}
			encoded, err := json.Marshal(item)
			require.NoError(t, err)
			assert.JSONEq(t, response, string(encoded))
			assert.Contains(t, string(encoded), "9007199254740993")
			require.Len(t, item.Results, 1)
			assert.Equal(t, ResultFailed, item.Results[0].Outcome(),
				"preserving service fields must not change verdict classification")
		})
	}
}

func TestOutputItemJSONKeepsLegacyModeledShapes(t *testing.T) {
	for _, response := range []string{
		`{"id":"1","run_id":"run","status":"completed"}`,
		`{"id":"2","run_id":"run","status":"completed","results":[{"name":"quality","score":0,"passed":false}]}`,
		`{"id":"3","run_id":"run","status":"errored","results":[{"name":"quality","score":null,"passed":null}]}`,
	} {
		var item OutputItem
		require.NoError(t, json.Unmarshal([]byte(response), &item))
		body, err := json.Marshal(item)
		require.NoError(t, err)
		assert.JSONEq(t, response, string(body))
	}
	body, err := json.Marshal(OutputItem{ID: "new"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"new","run_id":"","status":""}`, string(body))
}

func TestOutputItemJSONStillNormalizesStringScores(t *testing.T) {
	var item OutputItem
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"1","run_id":"run","status":"completed",
		"results":[{"name":"quality","score":"0.5","passed":true,"properties":{"kept":true}}]
	}`), &item))
	body, err := json.Marshal(item)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id":"1","run_id":"run","status":"completed",
		"results":[{"name":"quality","score":0.5,"passed":true,"properties":{"kept":true}}]
	}`, string(body))
}

func TestOutputItemJSONPreservesExactDatasetNumbers(t *testing.T) {
	for _, value := range []string{
		"9007199254740993",
		"-9007199254740993",
		"18446744073709551615",
		"0.12345678901234567890123456789",
		"1e400",
		"0",
	} {
		t.Run(value, func(t *testing.T) {
			var item OutputItem
			require.NoError(t, json.Unmarshal([]byte(`{
				"id":"1","run_id":"run","status":"completed",
				"datasource_item":{"value":`+value+`,"nested":[{"value":`+value+`}]},
				"results":[{"name":"quality","score":"0.5","passed":true}]
			}`), &item))
			require.Equal(t, json.Number(value), item.DataSourceItem["value"])
			encoded, err := json.Marshal(item)
			require.NoError(t, err)
			var document struct {
				Item struct {
					Value  json.RawMessage `json:"value"`
					Nested []struct {
						Value json.RawMessage `json:"value"`
					} `json:"nested"`
				} `json:"datasource_item"`
				Results []struct {
					Score json.RawMessage `json:"score"`
				} `json:"results"`
			}
			require.NoError(t, json.Unmarshal(encoded, &document))
			assert.Equal(t, value, string(document.Item.Value), "do not compare arbitrary numbers through float64")
			require.Len(t, document.Item.Nested, 1)
			assert.Equal(t, value, string(document.Item.Nested[0].Value))
			require.Len(t, document.Results, 1)
			assert.Equal(t, "0.5", string(document.Results[0].Score), "modeled score normalization remains unchanged")
		})
	}
}

func TestOutputItemJSONPreservesNumbersWhenKnownFieldsChange(t *testing.T) {
	var item OutputItem
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"1","run_id":"run","status":"completed",
		"datasource_item":{"value":9007199254740993,"empty":null,"zero":0},
		"future_field":{"value":-9007199254740993}
	}`), &item))
	item.Status = "failed"
	item.DataSourceItem["value"] = json.Number("9007199254740995")
	body, err := json.Marshal(item)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	assert.Equal(t, `"failed"`, string(fields["status"]))
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fields["datasource_item"], &data))
	assert.Equal(t, "9007199254740995", string(data["value"]))
	assert.Equal(t, "null", string(data["empty"]))
	assert.Equal(t, "0", string(data["zero"]))
	assert.Equal(t, `{"value":-9007199254740993}`, string(fields["future_field"]))
}

func TestOutputItemDecoderRejectsMalformedAndTrailingData(t *testing.T) {
	for _, input := range []string{
		`{"id":"1"`,
		`{"id":"1"}{"id":"2"}`,
		`{"id":"1"} true`,
		`{"id":"1"} garbage`,
		`{"id":"1","datasource_item":{"value":1e}}`,
	} {
		t.Run(input, func(t *testing.T) {
			item := OutputItem{ID: "unchanged"}
			require.Error(t, item.UnmarshalJSON([]byte(input)))
			assert.Equal(t, "unchanged", item.ID, "reject the document before replacing the existing value")
		})
	}
	var item OutputItem
	require.NoError(t, item.UnmarshalJSON([]byte(" \n{\"id\":\"1\"}\r\n\t")))
	assert.Equal(t, "1", item.ID)
}
