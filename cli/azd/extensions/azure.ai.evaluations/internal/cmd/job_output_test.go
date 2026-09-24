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

			var show bytes.Buffer
			require.NoError(t, emitJSON(&show, map[string]any{"job": job}))
			page, err := kind.listPage(t.Context(), ec, 10, "")
			require.NoError(t, err)
			require.Len(t, page.Data, 1)
			assert.Equal(t, "simulation_seed", page.Data[0].GenerationType())
			var list bytes.Buffer
			require.NoError(t, emitJSONPage(&list, page.Data, new(1), ""))

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
