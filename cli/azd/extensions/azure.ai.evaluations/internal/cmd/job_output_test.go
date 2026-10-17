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

func TestJobJSONOmitsEchoedInputs(t *testing.T) {
	const jobBody = `{
		"id":"job_1",
		"status":"succeeded",
		"inputs":{
			"name":"generated-data",
			"options":{"type":"simulation_seed"},
			"sources":[{"type":"prompt","prompt":"PRIVATE_GENERATION_PROMPT_fixture"}]
		},
		"result":{"name":"golden","version":"3"},
		"warnings":[{"code":"limited_input","message":"Limited input."}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/job_1") {
			_, _ = w.Write([]byte(jobBody))
			return
		}
		_, _ = w.Write([]byte(`{"data":[` + jobBody + `],"has_more":false}`))
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)

	for _, kind := range []jobKind{datasetJobs, evaluatorJobs} {
		t.Run(kind.name, func(t *testing.T) {
			job, err := kind.get(t.Context(), ec, "job_1")
			require.NoError(t, err)
			assert.Equal(t, "simulation_seed", job.GenerationType(), "reattachment still needs the input type")
			assert.Equal(t, "simulation_seed", jobForJSON(job).GenerationType())

			var show bytes.Buffer
			require.NoError(t, emitJSON(&show, map[string]any{"job": jobForJSON(job)}))
			page, err := kind.listPage(t.Context(), ec, 10, "")
			require.NoError(t, err)
			require.Len(t, page.Data, 1)
			assert.Equal(t, "simulation_seed", page.Data[0].GenerationType())
			var list bytes.Buffer
			require.NoError(t, emitJSONPage(&list, jobsForJSON(page.Data), new(1), ""))

			for _, out := range []string{show.String(), list.String()} {
				assert.NotContains(t, out, `"inputs"`)
				assert.NotContains(t, out, `"sources"`)
				assert.NotContains(t, out, "PRIVATE_GENERATION_PROMPT_fixture")
				assert.NotContains(t, out, "generated-data")
				var document map[string]any
				require.NoError(t, json.Unmarshal([]byte(out), &document))
				assert.Contains(t, out, `"job_1"`)
				assert.Contains(t, out, `"succeeded"`)
				assert.Contains(t, out, `"golden"`)
				assert.Contains(t, out, `"limited_input"`)
			}
		})
	}
}

func TestJobJSONRedactsServiceErrorsWithoutMutatingRecords(t *testing.T) {
	assert.Nil(t, jobForJSON(nil))
	assert.Nil(t, jobsForJSON(nil))
	for _, diagnosticURL := range []string{
		"https://fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
		"//" + "fixture-user:fixture-password@" + "host/file?sig=fixture-signature#fixture-fragment",
		"https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment",
	} {
		text, err := json.Marshal("download " + diagnosticURL)
		require.NoError(t, err)
		for shape, errorBody := range map[string]string{
			"bare string":        string(text),
			"structured message": `{"code":` + string(text) + `,"message":` + string(text) + `}`,
			"nested message":     `{"message":{"message":` + string(text) + `}}`,
		} {
			t.Run(diagnosticURL+"/"+shape, func(t *testing.T) {
				const result = `{"name":"golden","version":"3","value":9007199254740993}`
				jobBody := `{"id":"job_1","status":"failed","result":` + result +
					`,"error":` + errorBody +
					`,"inputs":{"sources":[{"type":"prompt","prompt":"PRIVATE_GENERATION_PROMPT_fixture"}]}}`
				var original eval_api.GenerationJob
				require.NoError(t, json.Unmarshal([]byte(jobBody), &original))
				before, err := json.Marshal(&original)
				require.NoError(t, err)
				display := jobForJSON(&original)
				require.NotSame(t, original.Error, display.Error)
				list := []eval_api.GenerationJob{original}
				projectedList := jobsForJSON(list)
				require.NotSame(t, list[0].Error, projectedList[0].Error)
				assert.Equal(t, original.Error.Message, list[0].Error.Message)
				assert.Equal(t, "download "+diagnosticURL, original.Error.Message)
				after, err := json.Marshal(&original)
				require.NoError(t, err)
				assert.Equal(t, string(before), string(after), "projection must not modify the service record")

				for _, kind := range []jobKind{datasetJobs, evaluatorJobs} {
					for _, operation := range []string{"show", "list", "list all", "cancel"} {
						t.Run(kind.name+"/"+operation, func(t *testing.T) {
							srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Content-Type", "application/json")
								if strings.HasSuffix(r.URL.Path, "/job_1") ||
									strings.HasSuffix(r.URL.Path, "/job_1:cancel") {
									_, _ = w.Write([]byte(jobBody))
									return
								}
								_, _ = w.Write([]byte(`{"data":[` + jobBody + `],"has_more":false}`))
							}))
							t.Cleanup(srv.Close)
							ec := evalContextFor(srv)
							cmd := jsonCmd(t, "json")
							cmd.SetContext(t.Context())
							var out bytes.Buffer
							cmd.SetOut(&out)
							switch operation {
							case "show":
								a := &jobShowAction{cmd: cmd, flags: &jobFlags{}, jobID: "job_1"}
								require.NoError(t, a.show(t.Context(), ec, kind))
							case "list", "list all":
								a := &jobListAction{cmd: cmd, flags: &jobListFlags{showAll: operation == "list all"}}
								require.NoError(t, a.list(t.Context(), ec, kind))
							case "cancel":
								a := &jobCancelAction{cmd: cmd, flags: &jobFlags{}, jobID: "job_1"}
								require.NoError(t, a.cancel(t.Context(), ec, kind))
							}
							for _, secret := range []string{
								"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment",
								"PRIVATE_GENERATION_PROMPT_fixture",
							} {
								assert.NotContains(t, out.String(), secret)
							}
							assert.True(t, json.Valid(out.Bytes()), "output remains one JSON document")
							assert.Contains(t, out.String(), `"job_1"`)
							assert.Contains(t, out.String(), `"failed"`)
							assert.Contains(t, out.String(), `"golden"`)
							assert.Contains(t, out.String(), "9007199254740993", "artifact precision is unchanged")
							assert.Contains(t, out.String(), "download ")
						})
					}
				}
			})
		}
	}
}
