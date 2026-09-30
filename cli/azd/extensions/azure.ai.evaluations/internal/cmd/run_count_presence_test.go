// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanRunViewsPreservePartialCountPresence(t *testing.T) {
	for _, render := range []struct {
		name string
		call func(io.Writer, *eval_api.OpenAIEvalRun) error
	}{
		{"summary", func(out io.Writer, run *eval_api.OpenAIEvalRun) error { return renderRun(out, run, nil) }},
		{"detail", renderRunDetail},
		{"output", func(out io.Writer, run *eval_api.OpenAIEvalRun) error {
			return renderResults(out, "eval_partial", run, nil, resultListView{})
		}},
	} {
		for _, counts := range []string{
			`{"total":2}`,
			`{"total":2,"passed":null,"failed":null,"errored":null,"skipped":null}`,
			`{"passed":2}`,
			`{}`,
		} {
			t.Run(render.name+"/"+counts, func(t *testing.T) {
				var run eval_api.OpenAIEvalRun
				require.NoError(t, json.Unmarshal([]byte(`{
					"id":"run_partial","eval_id":"eval_partial","status":"completed","result_counts":`+counts+`}`), &run))
				before, err := json.Marshal(run)
				require.NoError(t, err)
				var out bytes.Buffer
				require.NoError(t, render.call(&out, &run))
				text := out.String()
				assert.Contains(t, strings.ToLower(text), "not reported")
				for _, invented := range []string{
					"0 failed", "0 errored", "Errored       2", "Errored       0", "Failed        0",
				} {
					assert.NotContains(t, text, invented)
				}
				after, err := json.Marshal(run)
				require.NoError(t, err)
				assert.Equal(t, string(before), string(after))
			})
		}
	}
}

func TestHumanRunViewsKeepExplicitZeroCounts(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_zero","status":"completed",
		"result_counts":{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}}`), &run))
	var out bytes.Buffer
	require.NoError(t, renderRun(&out, &run, nil))
	assert.Contains(t, out.String(), messages.TestCaseResults(0, 0, 0, 0, 0, "-"))
	out.Reset()
	require.NoError(t, renderRunDetail(&out, &run))
	assert.Contains(t, out.String(), "0 passed, 0 failed, 0 errored")
	out.Reset()
	require.NoError(t, renderResults(&out, "eval_zero", &run, nil, resultListView{}))
	assert.Contains(t, out.String(), "0 test cases: 0 passed, 0 failed, 0 errored, 0 skipped")
}

func TestWaitedRunStartDistinguishesZeroAndUnreportedCounts(t *testing.T) {
	for _, counts := range []struct {
		name string
		json string
	}{
		{"zero", `,"result_counts":{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0}`},
		{"partial", `,"result_counts":{"total":0}`},
		{"null", `,"result_counts":null`},
		{"absent", ""},
	} {
		for _, status := range []string{"completed", "failed"} {
			for _, format := range []string{"table", "json"} {
				t.Run(counts.name+"/"+status+"/"+format, func(t *testing.T) {
					response := `{"id":"run_zero","status":"` + status + `"` + counts.json + `}`
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch {
						case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"id":"run_zero","status":"queued"}`)
						case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs/run_zero"):
							_, _ = io.WriteString(w, response)
						case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
						default:
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(srv.Close)
					var out bytes.Buffer
					command := jsonCmd(t, format)
					command.SetContext(t.Context())
					command.SetOut(&out)
					action := &runStartAction{cmd: command, flags: &runStartFlags{
						groupName: "eval_zero", evalPath: t.TempDir(), wait: true,
					}}
					err := action.start(t.Context(), evalContextFor(srv), gate{})
					if status == "failed" {
						require.ErrorContains(t, err, "run_zero finished with status failed")
					} else {
						require.NoError(t, err)
					}
					if format == "json" {
						assert.JSONEq(t, response, out.String())
						return
					}
					text := out.String()
					switch counts.name {
					case "zero":
						assert.Contains(t, text, messages.TestCaseResults(0, 0, 0, 0, 0, "-"))
					case "partial":
						assert.Contains(t, text, "TEST CASE RESULTS")
						assert.Contains(t, text, "Total         0")
						assert.Contains(t, text, "not reported")
						assert.NotContains(t, text, "Failed        0")
					default:
						assert.NotContains(t, text, "TEST CASE RESULTS")
					}
					assert.NotContains(t, text, "--failed-only")
					assert.NotContains(t, text, "--status errored")
				})
			}
		}
	}
}

func TestRunCallersRenderMissingCountMembersAsUnreported(t *testing.T) {
	const response = `{"id":"run_partial","status":"completed","result_counts":{"total":2},
		"unknown":{"value":9007199254740993}}`
	for _, caller := range []string{"start", "show", "output list"} {
		for _, format := range []string{"table", "json"} {
			t.Run(caller+"/"+format, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/output_items"):
						_, _ = io.WriteString(w, `{"data":[]}`)
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
						_, _ = io.WriteString(w, `{"id":"run_partial","status":"queued"}`)
					case strings.HasSuffix(r.URL.Path, "/runs/run_partial"):
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
				command := jsonCmd(t, format)
				command.SetContext(t.Context())
				command.SetOut(&out)
				ec := evalContextFor(srv)
				switch caller {
				case "start":
					action := &runStartAction{cmd: command, flags: &runStartFlags{
						groupName: "eval_partial", evalPath: t.TempDir(), wait: true,
					}}
					require.NoError(t, action.start(t.Context(), ec, gate{}))
				case "show":
					action := &runShowAction{cmd: command, runID: "run_partial", flags: &runShowFlags{}}
					require.NoError(t, action.show(t.Context(), ec, "eval_partial", gate{}))
				case "output list":
					action := &runOutputListAction{cmd: command, runID: "run_partial", flags: &runOutputListFlags{}}
					require.NoError(t, action.list(t.Context(), ec, "eval_partial"))
				}
				if format == "json" {
					if caller != "output list" {
						var document map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(out.Bytes(), &document))
						assert.JSONEq(t, `{"total":2}`, string(document["result_counts"]))
						assert.Contains(t, string(document["unknown"]), "9007199254740993")
					}
					return
				}
				text := out.String()
				assert.Contains(t, text, "not reported")
				for _, invented := range []string{"0 failed", "0 errored", "Errored       2", "Errored       0"} {
					assert.NotContains(t, text, invented)
				}
				assert.NotContains(t, text, "--status errored")
			})
		}
	}
}

func TestRunSummaryDoesNotInferErroredRowsOverExplicitZero(t *testing.T) {
	for _, status := range []string{"in_progress", "completed", ""} {
		run := &eval_api.OpenAIEvalRun{
			ID: "run_counts", EvalID: "eval_counts", Status: status,
			ResultCounts: &eval_api.EvalRunResultCounts{Total: 2},
		}
		var out bytes.Buffer
		require.NoError(t, renderRun(&out, run, nil))
		assert.Contains(t, out.String(), "Errored       0")
		assert.NotContains(t, out.String(), "Errored       2")
		assert.NotContains(t, out.String(), "--status errored")
	}
}

func TestRunListFieldsUseReportedCounts(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"run_partial","result_counts":{"passed":2}}`), &run))
	assert.Equal(t, "not reported", reportedSampleCount(&run))
	assert.Equal(t, "not reported", reportedRunPassRate(&run))
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"run_partial","result_counts":{"total":2,"passed":2,"failed":0}}`), &run))
	assert.Equal(t, "2", reportedSampleCount(&run))
	assert.Equal(t, "100.0%", reportedRunPassRate(&run))
}

func TestMovingGateUsesResolvedIDWithoutChangingJSON(t *testing.T) {
	const response = `{"status":"in_progress","result_counts":{"total":2}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/runs/run_resolved"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	for _, format := range []string{"table", "json"} {
		command := jsonCmd(t, format)
		command.SetContext(t.Context())
		var out bytes.Buffer
		command.SetOut(&out)
		action := &runShowAction{cmd: command, runID: "run_resolved", flags: &runShowFlags{}}
		err := action.show(t.Context(), evalContextFor(srv), "eval_resolved", gate{set: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run_resolved")
		assert.Contains(t, err.Error(), "in_progress")
		assert.Empty(t, out.String())
	}
}

func TestRunGateWithOnlyUnaccountedRowsStillFails(t *testing.T) {
	const helper = "AZD_TEST_UNACCOUNTED_GATE_DIR"
	const response = `{"id":"run_counts","status":"completed",
		"result_counts":{"total":3,"passed":0,"failed":0,"errored":0,"skipped":0}}`
	if os.Getenv(helper) == "" {
		binary, err := os.Executable()
		require.NoError(t, err)
		for _, caller := range []string{"start", "show"} {
			for _, format := range []string{"table", "json"} {
				t.Run(caller+"/"+format, func(t *testing.T) {
					dir := t.TempDir()
					child := exec.CommandContext(t.Context(), binary,
						"-test.run=^TestRunGateWithOnlyUnaccountedRowsStillFails$")
					child.Env = append(os.Environ(), helper+"="+dir,
						"AZD_TEST_GATE_CALLER="+caller, "AZD_TEST_GATE_FORMAT="+format, "NO_COLOR=1")
					output, err := child.CombinedOutput()
					exitErr, ok := errors.AsType[*exec.ExitError](err)
					require.True(t, ok, "expected gate exit, got %v: %s", err, output)
					assert.Equal(t, exitCodeGateBreached, exitErr.ExitCode(), "%s", output)
					stdout, err := os.ReadFile(filepath.Join(dir, "stdout.txt"))
					require.NoError(t, err)
					stderr, err := os.ReadFile(filepath.Join(dir, "stderr.txt"))
					require.NoError(t, err)
					assert.Contains(t, string(stderr),
						"3 of 3 rows are not accounted for by the reported counts; the pass-rate gate covers 0 scored rows")
					assert.Contains(t, string(stderr), "ERROR: evaluation quality gate not met.")
					assert.NotContains(t, string(stderr), "3 errored")
					if format == "json" {
						assert.JSONEq(t, response, string(stdout))
					} else if caller == "start" {
						assert.Contains(t, string(stdout), "Errored       0")
					} else {
						assert.Contains(t, string(stdout), "0 passed, 0 failed, 0 errored")
					}
				})
			}
		}
		return
	}

	dir := os.Getenv(helper)
	out, err := os.Create(filepath.Join(dir, "stdout.txt"))
	require.NoError(t, err)
	defer out.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr.txt"))
	require.NoError(t, err)
	defer stderr.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/run_counts"):
			_, _ = io.WriteString(w, response)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = io.WriteString(w, `{"id":"run_counts","status":"queued"}`)
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/output_items"):
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	command := jsonCmd(t, os.Getenv("AZD_TEST_GATE_FORMAT"))
	command.SetContext(t.Context())
	command.SetOut(out)
	command.SetErr(stderr)
	threshold, err := parseGate("pass-rate=0.5")
	require.NoError(t, err)
	if os.Getenv("AZD_TEST_GATE_CALLER") == "start" {
		action := &runStartAction{cmd: command, flags: &runStartFlags{
			groupName: "eval_counts", evalPath: dir, wait: true,
		}}
		require.NoError(t, action.start(t.Context(), evalContextFor(srv), threshold))
	} else {
		action := &runShowAction{cmd: command, runID: "run_counts", flags: &runShowFlags{}}
		require.NoError(t, action.show(t.Context(), evalContextFor(srv), "eval_counts", threshold))
	}
	t.Fatal("a run with no scored rows must not pass the gate")
}

func TestRunGateWarningsRespectReportedErrorCounts(t *testing.T) {
	for _, counts := range []struct {
		name, raw, warning string
	}{
		{"explicit zero", `{"total":10,"passed":5,"failed":2,"errored":0,"skipped":0}`,
			"3 of 10 rows are not accounted for by the reported counts; the pass-rate gate covers 7 scored rows"},
		{"reported errors", `{"total":10,"passed":5,"failed":2,"errored":1,"skipped":0}`,
			"2 of 10 rows are not accounted for by the reported counts; the pass-rate gate covers 7 scored rows"},
		{"reported skips", `{"total":10,"passed":5,"failed":2,"errored":0,"skipped":1}`,
			"2 of 10 rows are not accounted for by the reported counts; the pass-rate gate covers 7 scored rows"},
		{"legacy remainder", `{"total":10,"passed":5,"failed":2,"skipped":0}`, "3 errored of 10"},
		{"consistent errors", `{"total":10,"passed":5,"failed":2,"errored":3,"skipped":0}`, "3 errored of 10"},
		{"consistent scored", `{"total":7,"passed":5,"failed":2,"errored":0,"skipped":0}`, ""},
		{"partial", `{"total":10,"passed":5}`,
			"not all passed/failed counts were reported; the pass-rate gate used a denominator of 5 for 10 total rows"},
		{"null failed", `{"total":10,"passed":5,"failed":null}`,
			"not all passed/failed counts were reported; the pass-rate gate used a denominator of 5 for 10 total rows"},
	} {
		for _, caller := range []string{"start", "show"} {
			for _, format := range []string{"table", "json"} {
				t.Run(counts.name+"/"+caller+"/"+format, func(t *testing.T) {
					response := `{"id":"run_counts","status":"completed","result_counts":` + counts.raw + `}`
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch {
						case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"id":"run_counts","status":"queued"}`)
						case strings.HasSuffix(r.URL.Path, "/runs/run_counts"):
							_, _ = io.WriteString(w, response)
						case strings.HasSuffix(r.URL.Path, "/output_items"):
							_, _ = io.WriteString(w, `{"data":[]}`)
						case strings.HasSuffix(r.URL.Path, "/runs"):
							_, _ = io.WriteString(w, `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`)
						default:
							t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(srv.Close)
					command := jsonCmd(t, format)
					command.SetContext(t.Context())
					var out, stderr bytes.Buffer
					command.SetOut(&out)
					command.SetErr(&stderr)
					ec := evalContextFor(srv)
					threshold, err := parseGate("pass-rate=0.5")
					require.NoError(t, err)
					if caller == "start" {
						action := &runStartAction{cmd: command, flags: &runStartFlags{
							groupName: "eval_counts", evalPath: t.TempDir(), wait: true,
						}}
						require.NoError(t, action.start(t.Context(), ec, threshold))
					} else {
						action := &runShowAction{cmd: command, runID: "run_counts", flags: &runShowFlags{}}
						require.NoError(t, action.show(t.Context(), ec, "eval_counts", threshold))
					}
					if counts.warning == "" {
						assert.Empty(t, stderr.String(), "a fully accounted scored run needs no warning")
					} else {
						assert.Contains(t, stderr.String(), counts.warning)
						assert.Equal(t, 1, strings.Count(stderr.String(), "warning:"))
						if strings.Contains(counts.warning, "reported") {
							assert.NotContains(t, stderr.String(), "errored", "unaccounted rows are not reported errors")
						}
					}
					if format == "json" {
						assert.JSONEq(t, response, out.String())
					}
				})
			}
		}
	}
}
