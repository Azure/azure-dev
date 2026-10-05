// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunShowJSONRedactsKnownErrorDiagnosticsOnly(t *testing.T) {
	for _, diagnosticURL := range []string{
		"https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"https:fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		`https:\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
		"HtTpS://fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"//fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"url_https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"url_HtTpS:fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		`url_https:\fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment`,
		"url=https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"(url:https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment).",
		"https://host/file?sig= fixture-signature",
		"https://host/file?sig=\tfixture-signature",
		"https://host/file?sig=\nfixture-signature",
	} {
		t.Run(diagnosticURL, func(t *testing.T) {
			errorText, err := json.Marshal("Failed " + diagnosticURL)
			require.NoError(t, err)
			const userData = `{"source_url":"https://source.example/file?sig=opaque-user-data","value":9007199254740993}`
			response := `{"id":"run_failed","status":"failed",
				"error":{"code":` + string(errorText) + `,"message":` + string(errorText) + `,"unknown":9007199254740993},
				"result_counts":{"total":0,"failed":null},"user_data":` + userData + `}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_failed"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)
			var out bytes.Buffer
			command := jsonCmd(t, "json")
			command.SetContext(t.Context())
			command.SetOut(&out)
			action := &runShowAction{cmd: command, runID: "run_failed", flags: &runShowFlags{}}
			require.NoError(t, action.show(t.Context(), ec, "eval_failed", gate{}))
			var document struct {
				Error struct {
					Code, Message string
					Unknown       json.RawMessage `json:"unknown"`
				} `json:"error"`
				Counts   json.RawMessage `json:"result_counts"`
				UserData json.RawMessage `json:"user_data"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &document))
			for _, secret := range []string{
				"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment",
			} {
				assert.NotContains(t, document.Error.Code+document.Error.Message, secret)
			}
			assert.Contains(t, document.Error.Message, "Failed ")
			assert.Equal(t, "9007199254740993", string(document.Error.Unknown))
			assert.JSONEq(t, `{"total":0,"failed":null}`, string(document.Counts))
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, document.UserData))
			assert.Equal(t, userData, compact.String(), "unrelated user data and numeric precision are unchanged")

			var original eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(response), &original))
			projected := runForJSON(&original)
			assert.Equal(t, "Failed "+diagnosticURL, original.Error.Message)
			assert.NotSame(t, original.Error, projected.Error)
		})
	}
}

func TestReportingJSONCallersPreserveNestedResultPresence(t *testing.T) {
	const runResponse = `{"id":"run_presence","status":"completed","per_testing_criteria_results":[
		{"testing_criteria":"quality","passed":1,"failed":null,"unknown":9007199254740993},
		{"testing_criteria":"second","passed":0,"failed":0,"errored":0,"skipped":0}]}`
	const itemResponse = `{"id":"1","run_id":"run_presence","status":"completed","results":[
		{"name":"quality","properties":{"unknown":9007199254740993}},
		{"name":"second","score":null,"passed":null},
		{"name":"third","score":0,"passed":false}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/run_presence"):
			_, _ = w.Write([]byte(runResponse))
		case strings.HasSuffix(r.URL.Path, "/output_items/1"):
			_, _ = w.Write([]byte(itemResponse))
		case strings.HasSuffix(r.URL.Path, "/output_items"):
			_, _ = w.Write([]byte(`{"data":[` + itemResponse + `]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	for _, caller := range []string{"run show", "item show", "item list", "item file", "export"} {
		t.Run(caller, func(t *testing.T) {
			command := jsonCmd(t, "json")
			command.SetContext(t.Context())
			var out, stderr bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&stderr)
			ec := evalContextFor(srv)
			var gotRun, gotItem json.RawMessage
			switch caller {
			case "run show":
				action := &runShowAction{cmd: command, runID: "run_presence", flags: &runShowFlags{}}
				require.NoError(t, action.show(t.Context(), ec, "eval_presence", gate{}))
				gotRun = out.Bytes()
			case "item show":
				action := &runOutputShowAction{cmd: command, itemID: "1",
					flags: &runOutputShowFlags{run: "run_presence"}}
				require.NoError(t, action.showRun(t.Context(), ec, "eval_presence"))
				gotItem = out.Bytes()
			case "item list", "item file":
				flags := &runOutputListFlags{}
				if caller == "item file" {
					flags.outFile = filepath.Join(t.TempDir(), "items.json")
				}
				action := &runOutputListAction{cmd: command, runID: "run_presence", flags: flags}
				require.NoError(t, action.list(t.Context(), ec, "eval_presence"))
				var items []json.RawMessage
				if caller == "item file" {
					body, err := os.ReadFile(flags.outFile)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(body, &items))
				} else {
					var page struct {
						Items []json.RawMessage `json:"items"`
					}
					require.NoError(t, json.Unmarshal(out.Bytes(), &page))
					items = page.Items
				}
				require.Len(t, items, 1)
				gotItem = items[0]
			case "export":
				action := &runOutputExportAction{cmd: command, runID: "run_presence", flags: &runOutputExportFlags{}}
				require.NoError(t, action.export(t.Context(), ec, "eval_presence", exportToStdout))
				var doc exportDocument
				require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
				require.Len(t, doc.Items, 1)
				gotRun, gotItem = doc.Run, doc.Items[0]
			}
			assert.Empty(t, stderr.String())
			if gotRun != nil {
				assert.JSONEq(t, runResponse, string(gotRun))
				assert.Contains(t, string(gotRun), "9007199254740993")
			}
			if gotItem != nil {
				assert.JSONEq(t, itemResponse, string(gotItem))
				assert.Contains(t, string(gotItem), "9007199254740993")
			}
		})
	}
}

func TestWhitespaceSplitRunDiagnosticsAreSafeAtHumanAndExportCallers(t *testing.T) {
	for _, separator := range []string{" ", "\t", "\n", "\r\n"} {
		text := "Failed https://host/path?sig=" + separator + "fixture-secret; retry safely."
		encoded, err := json.Marshal(text)
		require.NoError(t, err)
		response := `{"id":"run_split","status":"failed","error":{"message":` + string(encoded) +
			`},"unknown":{"url":"https://host/?sig=retained-user-data","number":9007199254740993}}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/output_items") {
				_, _ = w.Write([]byte(`{"data":[]}`))
			} else {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_split"))
				_, _ = w.Write([]byte(response))
			}
		}))
		t.Cleanup(srv.Close)
		for _, caller := range []string{"human", "export"} {
			command := jsonCmd(t, "table")
			command.SetContext(t.Context())
			var out bytes.Buffer
			command.SetOut(&out)
			ec := evalContextFor(srv)
			if caller == "human" {
				action := &runShowAction{cmd: command, runID: "run_split", flags: &runShowFlags{}}
				require.NoError(t, action.show(t.Context(), ec, "eval_split", gate{}))
				assert.Contains(t, out.String(), "Failed <redacted-url>; retry safely.")
			} else {
				action := &runOutputExportAction{cmd: command, runID: "run_split", flags: &runOutputExportFlags{}}
				require.NoError(t, action.export(t.Context(), ec, "eval_split", exportToStdout))
				var doc struct {
					Run struct {
						Error struct {
							Message string `json:"message"`
						} `json:"error"`
						Unknown json.RawMessage `json:"unknown"`
					} `json:"run"`
				}
				require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
				assert.Equal(t, "Failed <redacted-url>; retry safely.", doc.Run.Error.Message)
				assert.Contains(t, string(doc.Run.Unknown), "retained-user-data")
				assert.Contains(t, string(doc.Run.Unknown), "9007199254740993")
			}
			assert.NotContains(t, out.String(), "fixture-secret")
		}
	}
}

func TestExportRedactsOnlyKnownRunErrorFields(t *testing.T) {
	const response = `{"id":"run_failed","error":{
		"message":"Failed https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"code":null,"unknown":9007199254740993},
		"result_counts":{"failed":null},"user_data":{"url":"https://user.example/?sig=keep","number":9007199254740993}}`
	const item = `{"id":"1","datasource_item":{"url":"https://data.example/?sig=keep","number":9007199254740993}}`
	for _, prefix := range []string{"", "url_", "url=", "(url:"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			original := strings.Replace(response, "Failed https:", "Failed "+prefix+"https:", 1)
			doc := exportDocument{Run: json.RawMessage(original), Items: []json.RawMessage{json.RawMessage(item)}}
			var out bytes.Buffer
			require.NoError(t, writeExport(&out, doc))
			var decoded exportDocument
			require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
			assert.NotContains(t, string(decoded.Run), "fixture-user")
			assert.NotContains(t, string(decoded.Run), "fixture-password")
			assert.NotContains(t, string(decoded.Run), "fixture-signature")
			assert.NotContains(t, string(decoded.Run), "fixture-fragment")
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, decoded.Run))
			assert.Contains(t, compact.String(), `"code":null`)
			assert.Contains(t, compact.String(), `"unknown":9007199254740993`)
			assert.Contains(t, compact.String(), `"result_counts":{"failed":null}`)
			assert.Contains(t, compact.String(),
				`"user_data":{"url":"https://user.example/?sig=keep","number":9007199254740993}`)
			require.Len(t, decoded.Items, 1)
			compact.Reset()
			require.NoError(t, json.Compact(&compact, decoded.Items[0]))
			assert.Equal(t, item, compact.String())
			assert.Equal(t, original, string(doc.Run), "copy-on-output must not mutate stored raw export data")
		})
	}
}

func TestExportErrorProjectionPreservesAbsentAndNullAndRejectsMalformed(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"error":null}`, `{"error":{}}`, `{"error":{"message":null}}`} {
		projected, err := redactExportRunError(json.RawMessage(raw))
		require.NoError(t, err)
		assert.Equal(t, raw, string(projected))
	}
	for _, raw := range []string{`{`, `{"error":[]}`, `{"error":{"message":{}}}`} {
		_, err := redactExportRunError(json.RawMessage(raw))
		require.Error(t, err)
	}
	assert.Nil(t, runForJSON(nil))
}

func TestRunJSONCallersPreserveInlineSourceNumbers(t *testing.T) {
	const response = `{"id":"run_numbers","status":"completed","created_at":1785575722.75,
		"data_source":{"type":"jsonl","source":{"type":"file_content","content":[{
			"large":9007199254740993,"decimal":0.12345678901234567890123456789,
			"nested":[-9007199254740993,1e400],"empty":null,"zero":0}]}},
		"result_counts":{"failed":null},"unknown":18446744073709551615}`
	for _, caller := range []string{"show", "show waited", "start", "export"} {
		t.Run(caller, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/runs/run_numbers"):
					_, _ = w.Write([]byte(response))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
					_, _ = w.Write([]byte(`{"id":"run_numbers","status":"queued"}`))
				case strings.HasSuffix(r.URL.Path, "/runs"):
					_, _ = w.Write([]byte(`{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`))
				case strings.HasSuffix(r.URL.Path, "/output_items"):
					_, _ = w.Write([]byte(`{"data":[]}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(srv.Close)
			var out bytes.Buffer
			command := jsonCmd(t, "json")
			command.SetContext(t.Context())
			command.SetOut(&out)
			ec := evalContextFor(srv)
			switch caller {
			case "start":
				action := &runStartAction{cmd: command, flags: &runStartFlags{
					groupName: "eval_numbers", evalPath: t.TempDir(), wait: true,
				}}
				require.NoError(t, action.start(t.Context(), ec, gate{}))
			case "export":
				action := &runOutputExportAction{cmd: command, runID: "run_numbers", flags: &runOutputExportFlags{}}
				require.NoError(t, action.export(t.Context(), ec, "eval_numbers", exportToStdout))
			default:
				action := &runShowAction{cmd: command, runID: "run_numbers",
					flags: &runShowFlags{wait: caller == "show waited"}}
				require.NoError(t, action.show(t.Context(), ec, "eval_numbers", gate{}))
			}
			body := out.Bytes()
			if caller == "export" {
				var exported exportDocument
				require.NoError(t, json.Unmarshal(body, &exported))
				body = exported.Run
			}
			var run struct {
				CreatedAt  json.RawMessage `json:"created_at"`
				Unknown    json.RawMessage `json:"unknown"`
				DataSource struct {
					Source struct {
						Content []map[string]json.RawMessage `json:"content"`
					} `json:"source"`
				} `json:"data_source"`
			}
			require.NoError(t, json.Unmarshal(body, &run))
			assert.Equal(t, "1785575722.75", string(run.CreatedAt))
			assert.Equal(t, "18446744073709551615", string(run.Unknown))
			require.Len(t, run.DataSource.Source.Content, 1)
			for key, want := range map[string]string{
				"large": "9007199254740993", "decimal": "0.12345678901234567890123456789",
				"nested": "[-9007199254740993,1e400]", "empty": "null", "zero": "0",
			} {
				var compact bytes.Buffer
				require.NoError(t, json.Compact(&compact, run.DataSource.Source.Content[0][key]))
				assert.Equal(t, want, compact.String(), key)
			}
		})
	}
}
