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
						assert.NotContains(t, err.Error(), "gate breached")
					}
					assert.Zero(t, outputRequests)
					assert.NotContains(t, stderr.String(), "fixture-password")
					if format == "json" {
						assert.JSONEq(t, string(response), out.String(),
							"emit exactly one unchanged service document, without injected identities or command prose")
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
	assert.Contains(t, out.String(), `--eval "declared evaluation" --run service_run`)
	assert.NotContains(t, out.String(), "fallback")
}

func TestCompletedConversationWithErroredRowOffersExplicitFilterAtCallSites(t *testing.T) {
	const response = `{
		"id":"run_completed","status":"completed","evaluation_level":"conversation",
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
