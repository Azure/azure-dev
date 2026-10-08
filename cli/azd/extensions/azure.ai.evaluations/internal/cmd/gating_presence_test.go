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

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGatesPreserveCountPresence(t *testing.T) {
	const helper = "AZD_TEST_GATE_PRESENCE_DIR"
	if os.Getenv(helper) == "" {
		binary, err := os.Executable()
		require.NoError(t, err)
		for _, tc := range []struct {
			name, counts, rate, anyFailure string
		}{
			{"absent counts", "", "indeterminate", "indeterminate"},
			{"null counts", `null`, "indeterminate", "indeterminate"},
			{"empty counts", `{}`, "indeterminate", "indeterminate"},
			{"absent total", `{"passed":1,"failed":0}`, "pass", "indeterminate"},
			{"null total", `{"total":null,"passed":1,"failed":0}`, "pass", "indeterminate"},
			{"only passed", `{"passed":1}`, "indeterminate", "indeterminate"},
			{"absent failed", `{"total":1,"passed":1}`, "indeterminate", "pass"},
			{"null failed", `{"total":1,"passed":1,"failed":null}`, "indeterminate", "pass"},
			{"absent passed", `{"total":1,"failed":0}`, "indeterminate", "indeterminate"},
			{"null passed", `{"total":1,"passed":null,"failed":0}`, "indeterminate", "indeterminate"},
			{"zero total", `{"total":0}`, "breach", "breach"},
			{"zero scored", `{"passed":0,"failed":0}`, "breach", "indeterminate"},
			{"known failure unknown total", `{"passed":0,"failed":1}`, "breach", "indeterminate"},
			{"all passed", `{"total":1,"passed":1,"failed":0}`, "pass", "pass"},
			{"unscored rows", `{"total":3,"passed":1,"failed":0,"errored":1,"skipped":1}`, "pass", "breach"},
		} {
			for _, spec := range []string{"", "pass-rate=0.5", "any-failure"} {
				outcome := "pass"
				switch spec {
				case "pass-rate=0.5":
					outcome = tc.rate
				case "any-failure":
					outcome = tc.anyFailure
				}
				for _, caller := range []string{"start", "show"} {
					for _, format := range []string{"table", "json"} {
						t.Run(tc.name+"/"+spec+"/"+caller+"/"+format, func(t *testing.T) {
							dir := t.TempDir()
							response := `{"id":"run_counts","status":"completed"`
							if tc.counts != "" {
								response += `,"result_counts":` + tc.counts
							}
							response += `}`
							child := exec.CommandContext(t.Context(), binary,
								"-test.run=^TestRunGatesPreserveCountPresence$")
							child.Env = append(os.Environ(), helper+"="+dir, "NO_COLOR=1",
								"AZD_TEST_GATE_RESPONSE="+response, "AZD_TEST_GATE_SPEC="+spec,
								"AZD_TEST_GATE_OUTCOME="+outcome,
								"AZD_TEST_GATE_CALLER="+caller, "AZD_TEST_GATE_FORMAT="+format)
							output, err := child.CombinedOutput()
							if outcome == "breach" {
								exitErr, ok := errors.AsType[*exec.ExitError](err)
								require.True(t, ok, "expected gate exit, got %v: %s", err, output)
								assert.Equal(t, exitCodeGateBreached, exitErr.ExitCode(), "%s", output)
							} else {
								require.NoError(t, err, "%s", output)
							}
							stdout, err := os.ReadFile(filepath.Join(dir, "stdout.txt"))
							require.NoError(t, err)
							if format == "json" {
								assert.JSONEq(t, response, string(stdout))
							}
							stderr, err := os.ReadFile(filepath.Join(dir, "stderr.txt"))
							require.NoError(t, err)
							if outcome == "breach" {
								assert.Contains(t, string(stderr), "ERROR: evaluation quality gate not met.")
							} else {
								assert.NotContains(t, string(stderr), "quality gate not met")
								assert.NotContains(t, string(stderr), "scored no rows")
							}
						})
					}
				}
			}
		}
		return
	}

	response := os.Getenv("AZD_TEST_GATE_RESPONSE")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/evals/eval_counts":
			_, _ = io.WriteString(w, `{"id":"eval_counts","data_source_config":{"type":"custom"}}`)
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
	dir := os.Getenv(helper)
	out, err := os.Create(filepath.Join(dir, "stdout.txt"))
	require.NoError(t, err)
	defer out.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr.txt"))
	require.NoError(t, err)
	defer stderr.Close()
	command := jsonCmd(t, os.Getenv("AZD_TEST_GATE_FORMAT"))
	command.SetContext(t.Context())
	command.SetOut(out)
	command.SetErr(stderr)
	threshold, err := parseGate(os.Getenv("AZD_TEST_GATE_SPEC"))
	require.NoError(t, err)
	if os.Getenv("AZD_TEST_GATE_CALLER") == "start" {
		action := &runStartAction{cmd: command, flags: &runStartFlags{
			groupName: "eval_counts", evalPath: dir, wait: true,
		}}
		err = action.start(t.Context(), evalContextFor(srv), threshold)
	} else {
		action := &runShowAction{cmd: command, runID: "run_counts", flags: &runShowFlags{}}
		err = action.show(t.Context(), evalContextFor(srv), "eval_counts", threshold)
	}
	switch os.Getenv("AZD_TEST_GATE_OUTCOME") {
	case "indeterminate":
		require.ErrorContains(t, err, "evaluation gate is indeterminate")
		assert.Contains(t, err.Error(), "result_counts did not report")
	case "pass":
		require.NoError(t, err)
	default:
		t.Fatal("a breached gate must exit with its quality code")
	}
}

func TestGateWithNoRunIsIndeterminateOnlyWhenRequested(t *testing.T) {
	command := jsonCmd(t, "table")
	var out bytes.Buffer
	command.SetErr(&out)
	require.NoError(t, applyGate(command, gate{}, nil))
	require.ErrorContains(t, applyGate(command, gate{set: true}, nil), "indeterminate")
	assert.Empty(t, out.String())

	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"result_counts":{"total":-1,"passed":1,"failed":0}}`), &run))
	reason, err := (gate{set: true, anyFailure: true}).evaluate(&run)
	require.ErrorContains(t, err, "did not report total")
	assert.Empty(t, reason)
}
