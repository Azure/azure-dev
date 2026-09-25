// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rubricPropertiesResponse = `{
	"id":"azureai://accounts/example/projects/example/aoaievaluationresults/result/versions/1",
	"run_id":"run_1","status":"completed",
	"results":[{
		"name":"quality","metric":"quality","score":0.75,"passed":true,"reason":"Overall explanation.",
		"sample":{"model":"judge","usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}},
		"properties":{"evaluator_version":"1","dimension_scores":[
			{"id":"dimension_1","score":4,"applicable":true,"weight":8,"reason":"First explanation.\nSecond line."},
			{"id":"dimension_2","score":0,"applicable":false,"weight":0,"reason":"Not applicable."},
			{"id":"dimension_3","score":3,"applicable":true,"weight":5,"reason":"Third explanation."},
			{"id":"dimension_4","score":4,"applicable":true,"weight":4,"reason":"Fourth explanation."},
			{"id":"dimension_5","score":2,"applicable":true,"weight":3,"reason":"Fifth explanation."},
			{"id":"dimension_6","score":4,"applicable":true,"weight":5,"reason":"Sixth explanation."}
		]}
	}]
}`

func TestOutputShowKeepsLookupIdentityAndReturnedRubricDimensions(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/output_items/1") {
					_, _ = w.Write([]byte(rubricPropertiesResponse))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":"1","run_id":"run_1","status":"completed"}]}`))
				}
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)
			page, err := ec.evalClient.ListOutputItemsPage(t.Context(), "eval_1", "run_1", 10, "")
			require.NoError(t, err)
			require.Len(t, page.Data, 1)
			require.Equal(t, "1", page.Data[0].ID)
			var out bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&out)
			format := ""
			if jsonOutput {
				format = "json"
			}
			command.Flags().String("output", format, "")
			action := &runOutputShowAction{cmd: command, itemID: page.Data[0].ID}
			require.NoError(t, action.show(t.Context(), ec, "eval_1", "run_1"))
			assert.Equal(t, []string{
				"/openai/v1/evals/eval_1/runs/run_1/output_items",
				"/openai/v1/evals/eval_1/runs/run_1/output_items/1",
			}, paths, "no extra lookup or billed work is needed")
			if jsonOutput {
				assert.JSONEq(t, rubricPropertiesResponse, out.String(),
					"service identity, dimensions and sample data survive the JSON call site")
			} else {
				text := out.String()
				assert.Contains(t, text, "Item ID          1\n", "the header keeps the lookup ID from the listing")
				assert.NotContains(t, text, "azureai://", "the service's result-version URI is not a lookup ID")
				assert.Contains(t, text, "RUBRIC DIMENSIONS")
				assert.Contains(t, text, "APPLICABLE")
				assert.Contains(t, text, "WEIGHT")
				assert.Contains(t, text, "0.75", "the evaluator's overall score remains separate")
				assert.Contains(t, text, "4.00")
				assert.Contains(t, text, "0.00", "a reported zero is not missing")
				assert.Contains(t, text, "false", "not applicable is not a failed verdict")
				assert.Contains(t, text, "First explanation.\nSecond line.", "preserve full dimension reasons")
				for i := range 6 {
					assert.Contains(t, text, fmt.Sprintf("dimension_%d", i+1))
				}
				assert.NotContains(t, text, "not returned by service")
				assert.NotContains(t, text, "DIMENSION   SCORE   RESULT",
					"the service did not return per-dimension pass/fail verdicts")
				assert.NotContains(t, text, `"prompt_tokens"`, "preserved machine details need not become human output")
			}
		})
	}
}

func TestRubricPropertyDimensionsDoNotInventMissingValues(t *testing.T) {
	var item eval_api.OutputItem
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"1","results":[{"name":"quality","score":0.75,"passed":true,"properties":{
			"dimension_scores":[
				{"id":"missing_values","reason":"No numeric values returned."},
				{"id":"null_values","score":null,"applicable":null,"weight":null},
				{"id":"zero_values","score":0,"applicable":false,"weight":0}
			]}}]
	}`), &item))
	var out bytes.Buffer
	require.NoError(t, renderOutputItem(&out, &item))
	rowsChecked := 0
	for line := range strings.SplitSeq(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "missing_values "), strings.HasPrefix(line, "null_values "):
			rowsChecked++
			assert.Equal(t, 3, strings.Count(line, "not reported"))
		case strings.HasPrefix(line, "zero_values "):
			rowsChecked++
			assert.Equal(t, 2, strings.Count(line, "0.00"))
			assert.Contains(t, line, "false")
		}
	}
	assert.Equal(t, 3, rowsChecked)
}

func TestMalformedRubricDimensionPropertiesRemainAvailableInJSON(t *testing.T) {
	for _, properties := range []string{
		`{"dimension_scores":"unexpected"}`,
		`{"dimension_scores":[{"id":"one","applicable":"not a boolean"}]}`,
		`{"dimension_scores":[{"id":"one","score":"not a number"}]}`,
	} {
		t.Run(properties, func(t *testing.T) {
			item := eval_api.OutputItem{ID: "1", Results: []eval_api.OutputResult{
				{Name: "quality", Properties: json.RawMessage(properties)},
			}}
			var out bytes.Buffer
			err := renderOutputItem(&out, &item)
			require.ErrorContains(t, err, "reading rubric dimension scores")
			out.Reset()
			require.NoError(t, emitJSON(&out, item), "JSON still carries the service value for diagnosis")
			var decoded struct {
				Results []struct {
					Properties json.RawMessage `json:"properties"`
				} `json:"results"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
			require.Len(t, decoded.Results, 1)
			assert.JSONEq(t, properties, string(decoded.Results[0].Properties))
		})
	}
}

func TestRubricPropertiesWithoutDimensionScoresKeepExistingHumanOutput(t *testing.T) {
	for _, properties := range []json.RawMessage{
		nil, json.RawMessage(`null`), json.RawMessage(`{}`),
		json.RawMessage(`{"dimension_scores":null}`), json.RawMessage(`{"dimension_scores":[]}`),
		json.RawMessage(`{"unrecognized_details":{"retained":true}}`),
	} {
		var out bytes.Buffer
		require.NoError(t, renderEvaluatorResult(&out, "quality", []eval_api.OutputResult{
			{Name: "quality", Score: 0.75, Passed: new(true), Properties: properties},
		}))
		assert.Contains(t, out.String(), "0.75")
		assert.NotContains(t, out.String(), "RUBRIC DIMENSIONS")
	}
}

func TestFilteredOutputJSONPreservesRubricPropertiesAndSample(t *testing.T) {
	var item eval_api.OutputItem
	require.NoError(t, json.Unmarshal([]byte(rubricPropertiesResponse), &item))
	items := filterItems([]eval_api.OutputItem{item}, map[string]bool{itemPassed: true})
	require.Len(t, items, 1)
	var out bytes.Buffer
	require.NoError(t, emitJSONPage(&out, items, nil, "next_item"))
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.JSONEq(t, rubricPropertiesResponse, string(page.Items[0]))
	out.Reset()
	require.NoError(t, emitJSONList(&out, items))
	assert.JSONEq(t, "["+rubricPropertiesResponse+"]", out.String(),
		"the output-file path retains the same service details as JSON stdout")
}

func TestOutputJSONCallersPreserveExactDatasetNumbers(t *testing.T) {
	const item = `{"id":"1","run_id":"run_numbers","status":"completed",
		"datasource_item":{"large_integer":9007199254740993,"nested":[
			0.12345678901234567890123456789,{"negative":-9007199254740993,"empty":null,"zero":0}]},
		"future_field":{"large_integer":9007199254740993,"nested":[-9007199254740993]},
		"results":[{"name":"quality","score":0.5,"passed":true}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/output_items/1") {
			_, _ = w.Write([]byte(item))
		} else {
			assert.True(t, strings.HasSuffix(r.URL.Path, "/output_items"))
			_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, item)
		}
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	for _, shape := range []string{"detail", "page", "file"} {
		t.Run(shape, func(t *testing.T) {
			var out bytes.Buffer
			if shape == "detail" {
				command := jsonCmd(t, "json")
				command.SetContext(t.Context())
				command.SetOut(&out)
				action := &runOutputShowAction{cmd: command, itemID: "1"}
				require.NoError(t, action.show(t.Context(), ec, "eval_numbers", "run_numbers"))
			} else {
				page, err := ec.evalClient.ListOutputItemsPage(t.Context(), "eval_numbers", "run_numbers", 10, "")
				require.NoError(t, err)
				rows := filterItems(page.Data, map[string]bool{itemPassed: true})
				require.Len(t, rows, 1)
				if shape == "page" {
					require.NoError(t, emitJSONPage(&out, rows, nil, ""))
				} else {
					require.NoError(t, emitJSONList(&out, rows))
				}
			}
			assert.Contains(t, out.String(), `"large_integer": 9007199254740993`)
			assert.Contains(t, out.String(), "0.12345678901234567890123456789")
			assert.Contains(t, out.String(), `"negative": -9007199254740993`)
			assert.Contains(t, out.String(), `"empty": null`)
			assert.Contains(t, out.String(), `"zero": 0`)
			assert.Contains(t, out.String(), `"future_field":`)
			assert.NotContains(t, out.String(), "9007199254740992")
		})
	}
}
