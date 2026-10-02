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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunShowOnlyOffersTerminalGuidanceAfterCompletion(t *testing.T) {
	for _, tc := range []struct {
		status   string
		terminal bool
	}{
		{"queued", false}, {"in_progress", false}, {"running", false}, {"cancelling", false},
		{"unrecognized", false}, {"completed", true}, {"failed", true}, {"cancelled", true},
		{"", true},
	} {
		for _, hasCounts := range []bool{false, true} {
			for _, format := range []string{"table", "json"} {
				t.Run(fmt.Sprintf("%s/counts=%t/%s", tc.status, hasCounts, format), func(t *testing.T) {
					payload := map[string]any{"id": "run_progress", "future_field": true}
					if tc.status != "" {
						payload["status"] = tc.status
					}
					if hasCounts {
						payload["result_counts"] = map[string]int{
							"total": 3, "passed": 1, "failed": 1, "errored": 1, "skipped": 0,
						}
					}
					response, err := json.Marshal(payload)
					require.NoError(t, err)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_progress"))
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(response)
					}))
					t.Cleanup(srv.Close)
					var out bytes.Buffer
					command := jsonCmd(t, format)
					command.SetContext(t.Context())
					command.SetOut(&out)
					action := &runShowAction{cmd: command, runID: "run_progress", flags: &runShowFlags{}}
					require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_progress", gate{}))
					if format == "json" {
						assert.JSONEq(t, string(response), out.String(), "status gating does not alter machine data")
						return
					}
					text := out.String()
					assert.Contains(t, text, "run_progress")
					if tc.status != "" {
						assert.Contains(t, text, tc.status)
					} else {
						assert.Contains(t, text, "not reported")
					}
					wantGuidance := tc.terminal && (tc.status != "" || hasCounts)
					assert.Equal(t, wantGuidance, strings.Contains(text, "run output export"))
					assert.Equal(t, wantGuidance, strings.Contains(text, "run output list"))
					assert.Equal(t, tc.terminal && hasCounts, strings.Contains(text, "--failed-only"))
					assert.Equal(t, tc.terminal && hasCounts, strings.Contains(text, "--status errored"))
				})
			}
		}
	}
}
