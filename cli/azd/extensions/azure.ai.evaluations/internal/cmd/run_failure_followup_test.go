// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runFailureWithCredentials = "Synthetic initialization failure. " +
	"Download https://fixture-user:fixture-password@storage.example/rows.jsonl?sig=fixture-signature#fixture-fragment"

func TestOperationalRunFailureOffersAvailableResultsWithoutClaimingRows(t *testing.T) {
	for _, render := range []struct {
		name string
		call func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(w io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(w, run, nil) }},
		{"detail", renderRunDetail},
	} {
		for _, tc := range []struct {
			name   string
			status string
			counts *eval_api.EvalRunResultCounts
			err    *eval_api.JobError
		}{
			{"failed without counts", "failed", nil, nil},
			{"failed with zero counts", "FAILED", &eval_api.EvalRunResultCounts{}, nil},
			{"error without counts", "error", nil, &eval_api.JobError{Message: "Unable to initialize evaluation."}},
			{"service error without status", "", nil, &eval_api.JobError{Code: "InitializationFailed"}},
			{"failure after partial scoring", "failed", &eval_api.EvalRunResultCounts{Total: 2, Passed: 1, Failed: 1}, nil},
			{"failure with errored rows", "failed", &eval_api.EvalRunResultCounts{Total: 1, Errored: 1}, nil},
		} {
			t.Run(render.name+"/"+tc.name, func(t *testing.T) {
				run := &eval_api.OpenAIEvalRun{
					ID: "run_failed", EvalID: "eval_resolved", Status: tc.status,
					ResultCounts: tc.counts, Error: tc.err,
				}
				var out bytes.Buffer
				require.NoError(t, render.call(&out, run))
				text := out.String()
				assert.Contains(t, text, "azd ai eval run output list --eval eval_resolved --run run_failed\n")
				assert.Contains(t, text,
					"azd ai eval run output export --eval eval_resolved --run run_failed --output-file ./run_failed.json")
				assert.Contains(t, text, "available")
				assert.Contains(t, text, "diagnostics")
				assert.Equal(t, tc.counts != nil && tc.counts.Failed > 0, strings.Contains(text, "--failed-only"),
					"only reported failed verdicts justify a failed-only listing")
				assert.Equal(t, tc.counts != nil && tc.counts.Errored > 0, strings.Contains(text, "--status errored"),
					"a run-level error alone does not establish errored output rows")
				assert.NotContains(t, text, "Rows that errored were never scored",
					"the run-level error does not establish that output rows exist")
				if tc.err != nil && tc.err.Message != "" {
					assert.Contains(t, text, tc.err.Message)
				}
				if tc.err != nil && tc.err.Code != "" {
					assert.Contains(t, text, tc.err.Code)
				}
			})
		}
	}
}

func TestFailedRunFollowUpNeverPrintsUnresolvedCommands(t *testing.T) {
	for _, run := range []*eval_api.OpenAIEvalRun{
		{ID: "run_missing_eval", Status: "failed"},
		{EvalID: "eval_missing_run", Status: "error"},
		{Status: "failed"},
	} {
		var out bytes.Buffer
		require.NoError(t, renderRunDetail(&out, run))
		assert.NotContains(t, out.String(), "azd ai eval run output")
		assert.Contains(t, out.String(), "eval ID and run ID")
	}
}

func TestRunDetailFollowUpDistinguishesQualityAndExecutionFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		run    *eval_api.OpenAIEvalRun
		failed bool
		all    bool
	}{
		{"quality failures", &eval_api.OpenAIEvalRun{Status: "completed",
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Failed: 1}}, true, true},
		{"errored rows", &eval_api.OpenAIEvalRun{Status: "completed",
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}}, false, true},
		{"mixed rows", &eval_api.OpenAIEvalRun{Status: "completed",
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 3, Passed: 1, Failed: 1, Errored: 1}}, true, true},
		{"successful empty run", &eval_api.OpenAIEvalRun{Status: "completed",
			ResultCounts: &eval_api.EvalRunResultCounts{}}, false, true},
		{"all passed", &eval_api.OpenAIEvalRun{Status: "completed",
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 2, Passed: 2}}, false, true},
		{"canceled before output", &eval_api.OpenAIEvalRun{Status: "canceled"}, false, true},
		{"queued", &eval_api.OpenAIEvalRun{Status: "queued"}, false, false},
		{"empty success error object", &eval_api.OpenAIEvalRun{
			Status: "completed", Error: &eval_api.JobError{},
		}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.ID, tc.run.EvalID = "run_rows", "eval_rows"
			var out bytes.Buffer
			require.NoError(t, renderRunDetail(&out, tc.run))
			text := out.String()
			assert.Equal(t, tc.failed, strings.Contains(text, "--failed-only"))
			assert.Equal(t, tc.run.ResultCounts != nil && tc.run.ResultCounts.Errored > 0,
				strings.Contains(text, "--status errored"))
			assert.Equal(t, tc.all, strings.Contains(text,
				"azd ai eval run output list --eval eval_rows --run run_rows\n"))
			assert.Equal(t, tc.failed || tc.all, strings.Contains(text, "azd ai eval run output export"))
		})
	}
}

func TestFailedRunCallersPreserveJSONAndPrintResolvedHumanCommands(t *testing.T) {
	for _, caller := range []string{"start", "show", "show waited", "show gated"} {
		for _, format := range []string{"table", "json"} {
			for _, counts := range []string{"absent", "zero"} {
				t.Run(caller+"/"+format+"/"+counts, func(t *testing.T) {
					payload := map[string]any{
						"id": "", "status": "failed",
						"metadata": map[string]string{metaEvalName: "mutable-friendly-name"},
						"error": map[string]string{
							"code": "RunInitializationFailed", "message": runFailureWithCredentials,
						},
						"diagnostic_field": map[string]any{"preserved": true},
					}
					if counts == "zero" {
						payload["result_counts"] = &eval_api.EvalRunResultCounts{}
					}
					response, err := json.Marshal(payload)
					require.NoError(t, err)
					outputRequests := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch {
						case strings.HasSuffix(r.URL.Path, "/output_items"):
							outputRequests++
							t.Error("a failed run with no output counts must not fetch rows to print next steps")
							w.WriteHeader(http.StatusNotFound)
						case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"id":"run_resolved","status":"queued"}`)
						case strings.HasSuffix(r.URL.Path, "/runs/run_resolved"):
							_, _ = w.Write(response)
						case strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
						default:
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(srv.Close)
					var out, stderr bytes.Buffer
					command := &cobra.Command{Use: "test-command", SilenceErrors: true, SilenceUsage: true}
					command.SetOut(&out)
					command.SetErr(&stderr)
					command.Flags().String("output", format, "")
					dir := t.TempDir()
					var actionErr error
					command.RunE = func(cmd *cobra.Command, _ []string) error {
						ec := evalContextFor(srv)
						if caller == "start" {
							action := &runStartAction{cmd: cmd, flags: &runStartFlags{
								groupName: "eval_resolved", evalPath: dir, wait: true,
							}}
							actionErr = action.start(cmd.Context(), ec, gate{})
						} else {
							action := &runShowAction{cmd: cmd, runID: "run_resolved", flags: &runShowFlags{
								wait: caller == "show waited",
							}}
							threshold := gate{set: caller == "show gated", anyFailure: true}
							actionErr = action.show(cmd.Context(), ec, "eval_resolved", threshold)
						}
						if actionErr != nil {
							return fmt.Errorf("command wrapper: %w", actionErr)
						}
						return nil
					}
					reportFailuresAsJSON(command)
					err = command.ExecuteContext(t.Context())
					if caller == "show" {
						require.NoError(t, err, "plain show is an inspection even when the run failed")
					} else {
						require.Error(t, actionErr)
						require.ErrorIs(t, err, actionErr, "operational errors remain errors through the command wrapper")
						assert.Contains(t, err.Error(), "finished with status failed")
						assert.Contains(t, err.Error(), "run_resolved",
							"returned diagnostics retain the successful lookup ID")
						assert.NotContains(t, err.Error(), "gate breached")
					}
					assert.Zero(t, outputRequests)
					assert.NotContains(t, stderr.String(), "fixture-password")
					if format == "json" {
						expected := strings.Replace(string(response), runFailureWithCredentials,
							"Synthetic initialization failure. Download https://storage.example/rows.jsonl", 1)
						assert.JSONEq(t, expected, out.String(),
							"emit one service document with only known error diagnostics redacted")
						assert.NotContains(t, out.String(), "azd ai eval")
						if actionErr != nil {
							assert.Contains(t, stderr.String(), "command wrapper:")
						}
					} else {
						text := out.String()
						assert.Contains(t, text, "Synthetic initialization failure.")
						assert.Contains(t, text, "https://storage.example/rows.jsonl")
						for _, secret := range []string{
							"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment",
						} {
							assert.NotContains(t, text, secret)
						}
						assert.Contains(t, text,
							"azd ai eval run output list --eval eval_resolved --run run_resolved\n")
						assert.Contains(t, text, "azd ai eval run output export --eval eval_resolved --run run_resolved "+
							"--output-file ./run_resolved.json")
						assert.NotContains(t, text, "--failed-only")
						assert.NotContains(t, text, "--eval mutable-friendly-name")
						assert.Equal(t, 1, strings.Count(text, "azd ai eval run output export"))
					}
				})
			}
		}
	}
}

func TestSimulationFailureFollowUpIsPrintedOnceAndPreservesUnknowns(t *testing.T) {
	run := simulationReportingRun()
	run.Status = "failed"
	run.ResultCounts = nil
	run.Error = &eval_api.JobError{Message: "Synthetic simulation initialization failure."}
	var out bytes.Buffer
	require.NoError(t, renderRunDetail(&out, run))
	assert.Equal(t, 1, strings.Count(out.String(), "azd ai eval run output export"))
	assert.Equal(t, 1, strings.Count(out.String(), "Synthetic simulation initialization failure."))
	assert.Contains(t, out.String(), "Conversations generated  not reported")
	assert.NotContains(t, out.String(), "--failed-only")
}

func TestRunDisplayIdentityFallbackDoesNotMutateServiceResponse(t *testing.T) {
	serviceRun := &eval_api.OpenAIEvalRun{Status: "failed"}
	display := runForDisplay(serviceRun, "eval_resolved", "run_resolved")
	assert.Equal(t, "eval_resolved", display.EvalID)
	assert.Equal(t, "run_resolved", display.ID)
	assert.Empty(t, serviceRun.EvalID)
	assert.Empty(t, serviceRun.ID)

	serviceRun.ID, serviceRun.EvalID = "service_run", "service_eval"
	serviceRun.Metadata = map[string]string{metaEvalName: "declared evaluation"}
	display = runForDisplay(serviceRun, "fallback_eval", "fallback_run")
	var out bytes.Buffer
	require.NoError(t, renderRunDetail(&out, display))
	assert.Contains(t, out.String(), "--eval service_eval --run service_run")
	assert.NotContains(t, out.String(), `--eval "declared evaluation"`)
	assert.NotContains(t, out.String(), "fallback")
}

func TestCompletedConversationWithErroredRowOffersExplicitFilterAtCallSites(t *testing.T) {
	const response = `{
		"id":"run_completed","status":"completed","evaluation_level":"conversation",
		"metadata":{"azd_eval":"mutable-friendly-name"},
		"data_source":{"type":"jsonl"},"error":null,
		"result_counts":{"total":1,"passed":0,"failed":0,"errored":1,"skipped":0}
	}`
	for _, caller := range []string{"start", "show", "show waited"} {
		for _, format := range []string{"table", "json"} {
			t.Run(caller+"/"+format, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/output_items"):
						_, _ = io.WriteString(w, `{"data":[{"id":"1","run_id":"run_completed","status":"completed",
							"results":[{"name":"quality","status":"errored","score":null,"passed":null}]}]}`)
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
						_, _ = io.WriteString(w, `{"id":"run_completed","status":"queued"}`)
					case strings.HasSuffix(r.URL.Path, "/runs/run_completed"):
						_, _ = io.WriteString(w, response)
					case strings.HasSuffix(r.URL.Path, "/runs"):
						_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				t.Cleanup(srv.Close)
				var out bytes.Buffer
				command := &cobra.Command{}
				command.SetContext(t.Context())
				command.SetOut(&out)
				command.Flags().String("output", format, "")
				ec := evalContextFor(srv)
				if caller == "start" {
					action := &runStartAction{cmd: command, flags: &runStartFlags{
						groupName: "eval_resolved", evalPath: t.TempDir(), wait: true,
					}}
					require.NoError(t, action.start(t.Context(), ec, gate{}))
				} else {
					action := &runShowAction{cmd: command, runID: "run_completed", flags: &runShowFlags{
						wait: caller == "show waited",
					}}
					require.NoError(t, action.show(t.Context(), ec, "eval_resolved", gate{}))
				}
				if format == "json" {
					assert.JSONEq(t, response, out.String())
				} else {
					assert.Contains(t, out.String(),
						"azd ai eval run output list --eval eval_resolved --run run_completed\n")
					assert.Contains(t, out.String(),
						"azd ai eval run output list --eval eval_resolved --run run_completed --status errored\n")
					assert.Contains(t, out.String(), "azd ai eval run output export --eval eval_resolved "+
						"--run run_completed --output-file ./run_completed.json")
					assert.NotContains(t, out.String(), "--failed-only",
						"an errored conversation has no failed verdict to filter")
					assert.NotContains(t, out.String(), "--eval mutable-friendly-name")
				}
			})
		}
	}
}

func TestRunFailureHumanOutputRedactsURLsWithoutMutatingJSON(t *testing.T) {
	for _, render := range []struct {
		name string
		call func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(w io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(w, run, nil) }},
		{"detail", renderRunDetail},
	} {
		for _, errorField := range []string{"message", "code"} {
			for _, simulation := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/simulation=%t", render.name, errorField, simulation), func(t *testing.T) {
					run := &eval_api.OpenAIEvalRun{
						ID: "run_failed", EvalID: "eval_failed", Status: "failed", Error: &eval_api.JobError{},
					}
					if simulation {
						run.DataSource = eval_api.NewSimulationDataSource("agent", "model", 1, 0)
					}
					if errorField == "code" {
						run.Error.Code = runFailureWithCredentials
					} else {
						run.Error.Message = runFailureWithCredentials
					}
					before, err := json.Marshal(run)
					require.NoError(t, err)
					var out bytes.Buffer
					require.NoError(t, render.call(&out, run))
					text := out.String()
					assert.Contains(t, text, "Synthetic initialization failure.")
					assert.Contains(t, text, "https://storage.example/rows.jsonl")
					for _, secret := range []string{
						"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig=",
					} {
						assert.NotContains(t, text, secret)
					}
					after, err := json.Marshal(run)
					require.NoError(t, err)
					assert.JSONEq(t, string(before), string(after),
						"redaction is a human presentation concern, not a rewrite of service JSON")
				})
			}
		}

	}
}

func TestRunFailureRedactsAdjacentURLs(t *testing.T) {
	//nolint:gosec // Synthetic URL credentials verify non-disclosure; this fixture contains no real secret.
	const message = `{"primary":"https://safe.example/a","secondary":"https://fixture-user:fixture-password@host/b"}`
	run := &eval_api.OpenAIEvalRun{
		ID: "run_failed", EvalID: "eval_failed", Status: "failed", Error: &eval_api.JobError{Message: message},
	}
	for _, render := range []func(io.Writer, *eval_api.OpenAIEvalRun) error{
		renderRunDetail,
		func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) },
	} {
		var out bytes.Buffer
		require.NoError(t, render(&out, run))
		assert.Contains(t, out.String(), "<redacted-url>")
		assert.NotContains(t, out.String(), "fixture-user")
		assert.NotContains(t, out.String(), "fixture-password")
		assert.Equal(t, message, run.Error.Message, "human redaction must not rewrite the service response")
	}
}

func TestPartialServiceCountsDoNotInventErroredFollowUps(t *testing.T) {
	for _, tc := range []struct {
		name, counts string
		errored      bool
	}{
		{"only total", `{"total":2}`, false},
		{"null passed", `{"total":2,"passed":null,"failed":0,"skipped":0}`, false},
		{"missing failed", `{"total":2,"passed":0,"skipped":0}`, false},
		{"missing skipped", `{"total":2,"passed":0,"failed":0}`, false},
		{"explicit errored", `{"errored":1}`, true},
		{"known remainder", `{"total":2,"passed":1,"failed":0,"skipped":0}`, true},
		{"fully scored", `{"total":2,"passed":2,"failed":0,"skipped":0}`, false},
		{"negative member", `{"total":2,"passed":-1,"failed":0,"skipped":0}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var run eval_api.OpenAIEvalRun
			require.NoError(t, json.Unmarshal([]byte(`{
				"id":"run_partial","eval_id":"eval_partial","status":"completed",
				"result_counts":`+tc.counts+`}`), &run))
			for _, render := range []func(io.Writer, *eval_api.OpenAIEvalRun) error{
				renderRunDetail,
				func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) },
			} {
				var out bytes.Buffer
				require.NoError(t, render(&out, &run))
				assert.Equal(t, tc.errored, strings.Contains(out.String(), "--status errored"))
				assert.NotContains(t, out.String(), "--failed-only")
				assert.Contains(t, out.String(), "--eval eval_partial --run run_partial")
				assert.Contains(t, out.String(), "run output export")
			}
		})
	}
}

func TestMovingRunDoesNotOfferTerminalFollowUps(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "running", "cancelling", "unrecognized"} {
		t.Run(status, func(t *testing.T) {
			run := &eval_api.OpenAIEvalRun{
				ID: "run_moving", EvalID: "eval_moving", Status: status,
				ResultCounts: &eval_api.EvalRunResultCounts{Total: 3, Failed: 1, Errored: 1},
			}
			for _, render := range []func(io.Writer, *eval_api.OpenAIEvalRun) error{
				renderRunDetail,
				func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) },
			} {
				var out bytes.Buffer
				require.NoError(t, render(&out, run))
				assert.Contains(t, out.String(), status)
				assert.NotContains(t, out.String(), "run output export")
				assert.NotContains(t, out.String(), "run output list")
			}
		})
	}
}

func TestRunShowPreservesPartialServiceCounts(t *testing.T) {
	for _, counts := range []string{
		`"result_counts":null`,
		`"result_counts":{}`,
		`"result_counts":{"total":2}`,
		`"result_counts":{"total":2,"passed":null,"failed":null,"future_count":7}`,
		`"result_counts":{"total":0,"passed":0,"failed":0}`,
		`"other_field":"no result_counts"`,
	} {
		for _, format := range []string{"json", "table"} {
			t.Run(format+"/"+counts, func(t *testing.T) {
				response := `{"id":"run_partial","status":"completed",` + counts + `}`
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_partial"))
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, response)
				}))
				t.Cleanup(srv.Close)
				var out bytes.Buffer
				command := jsonCmd(t, format)
				command.SetContext(t.Context())
				command.SetOut(&out)
				action := &runShowAction{cmd: command, runID: "run_partial", flags: &runShowFlags{}}
				require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_partial", gate{}))
				if format == "json" {
					assert.JSONEq(t, response, out.String())
				} else {
					assert.NotContains(t, out.String(), "--status errored")
					assert.NotContains(t, out.String(), "--failed-only")
					assert.Contains(t, out.String(), "--eval eval_partial --run run_partial")
					assert.Contains(t, out.String(), "run output export")
				}
			})
		}
	}

}

func TestRunFailureRedactsMalformedURLs(t *testing.T) {
	const malformed = "https:/fixture-user:fixture-password@host/file?sig=fixture-signature#fixture-fragment"
	for _, message := range []string{
		"Failed " + malformed,
		"Failed url_" + malformed,
		"Failed url=" + malformed,
		"Failed (url:" + malformed + ").",
		`{"primary":"https://safe.example/a","secondary":"` + malformed + `"}`,
	} {
		for _, render := range []func(io.Writer, *eval_api.OpenAIEvalRun) error{
			renderRunDetail,
			func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) },
		} {
			run := &eval_api.OpenAIEvalRun{
				ID: "run_failed", EvalID: "eval_failed", Status: "failed", Error: &eval_api.JobError{Message: message},
			}
			before, err := json.Marshal(run)
			require.NoError(t, err)
			var out bytes.Buffer
			require.NoError(t, render(&out, run))
			assert.Contains(t, out.String(), "<redacted-url>")
			for _, secret := range []string{
				"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment",
			} {
				assert.NotContains(t, out.String(), secret)
			}
			after, err := json.Marshal(run)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "human redaction must not mutate raw service JSON")
		}
	}
}
