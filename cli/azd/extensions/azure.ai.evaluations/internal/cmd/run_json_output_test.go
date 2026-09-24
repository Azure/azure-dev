// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestExportRedactsOnlyKnownRunErrorFields(t *testing.T) {
	const response = `{"id":"run_failed","error":{
		"message":"Failed https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"code":null,"unknown":9007199254740993},
		"result_counts":{"failed":null},"user_data":{"url":"https://user.example/?sig=keep","number":9007199254740993}}`
	const item = `{"id":"1","datasource_item":{"url":"https://data.example/?sig=keep","number":9007199254740993}}`
	for _, prefix := range []string{"", "url_"} {
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
