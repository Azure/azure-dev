// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyRunGuidanceUsesReportedCountEvidence(t *testing.T) {
	for _, tc := range []struct {
		counts   string
		guidance bool
	}{
		{`null`, false},
		{`{}`, false},
		{`{"total":null,"passed":null,"failed":null}`, false},
		{`{"total":2,"passed":2,"failed":0,"errored":0,"skipped":0}`, true},
		{`{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}`, true},
		{`{"total":0}`, true},
		{`{"passed":2}`, true},
	} {
		t.Run(tc.counts, func(t *testing.T) {
			response := `{"id":"run_legacy","result_counts":` + tc.counts + `,"unknown":{"keep":true}}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_legacy"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(srv.Close)
			for _, format := range []string{"json", "table"} {
				var out bytes.Buffer
				command := jsonCmd(t, format)
				command.SetContext(t.Context())
				command.SetOut(&out)
				action := &runShowAction{cmd: command, runID: "run_legacy", flags: &runShowFlags{}}
				require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_legacy", gate{}))
				if format == "json" {
					assert.JSONEq(t, response, out.String())
				} else {
					assert.Equal(t, tc.guidance, strings.Contains(out.String(), "run output list"))
					assert.Equal(t, tc.guidance, strings.Contains(out.String(), "run output export"))
					assert.NotContains(t, out.String(), "--failed-only")
					assert.NotContains(t, out.String(), "--status errored")
					assert.NotContains(t, out.String(), "complete results")
					assert.NotContains(t, out.String(), "Full run:")
					if tc.guidance {
						assert.Contains(t, out.String(), "Export available results:")
					}
				}
			}
		})
	}
}
