// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runBudgetTransport struct {
	t          *testing.T
	caller     string
	wrapped    bool
	getRunCall int
}

const runWithoutID = `{"status":"in_progress","unknown":{"preserved":true}}`

func (t *runBudgetTransport) Do(req *http.Request) (*http.Response, error) {
	body := ""
	switch {
	case strings.HasSuffix(req.URL.Path, "/runs/run_resolved"):
		t.getRunCall++
		if t.caller == "start" || t.getRunCall > 1 {
			if t.wrapped {
				return nil, fmt.Errorf("synthetic poll budget: %w", errWaitBudgetSpent)
			}
			return nil, errWaitBudgetSpent
		}
		body = runWithoutID
	case strings.HasSuffix(req.URL.Path, "/runs") && req.Method == http.MethodPost:
		body = `{"id":"run_resolved","status":"queued"}`
	case strings.HasSuffix(req.URL.Path, "/runs") && req.Method == http.MethodGet:
		body = `{"data":[{"id":"previous","data_source":{"type":"jsonl"}}]}`
	default:
		t.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		return nil, fmt.Errorf("unexpected synthetic request")
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: req,
	}, nil
}

func TestRunWaitBudgetDiagnosticsRetainResolvedIdentity(t *testing.T) {
	for _, caller := range []string{"start", "show"} {
		for _, format := range []string{"table", "json"} {
			for _, gated := range []bool{false, true} {
				for _, wrapped := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/gate=%t/wrapped=%t", caller, format, gated, wrapped), func(t *testing.T) {
						transport := &runBudgetTransport{t: t, caller: caller, wrapped: wrapped}
						pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
							Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
						})
						ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline("http://fixture.local", pipeline)}
						var out bytes.Buffer
						command := jsonCmd(t, format)
						command.SetContext(t.Context())
						command.SetOut(&out)
						threshold := gate{set: gated, anyFailure: true}
						var err error
						if caller == "start" {
							action := &runStartAction{cmd: command, flags: &runStartFlags{
								groupName: "eval_resolved", evalPath: t.TempDir(), wait: true,
							}}
							err = action.start(t.Context(), ec, threshold)
							assert.Equal(t, 1, transport.getRunCall)
						} else {
							action := &runShowAction{cmd: command, runID: "run_resolved", flags: &runShowFlags{wait: true}}
							err = action.show(t.Context(), ec, "eval_resolved", threshold)
							assert.Equal(t, 2, transport.getRunCall)
						}
						if gated {
							require.Error(t, err)
							assert.Contains(t, err.Error(), "run_resolved")
							assert.Contains(t, err.Error(), "--fail-on")
							return
						}
						require.NoError(t, err)
						if format == "json" {
							if caller == "show" {
								assert.JSONEq(t, runWithoutID, out.String(), "the timeout fallback preserves the raw run")
							} else {
								assert.JSONEq(t, `{"run_id":"run_resolved","eval_id":"eval_resolved","status":"queued"}`,
									out.String(), "the timeout handoff preserves both identities")
							}
						} else {
							assert.Contains(t, out.String(), messages.WaitBudgetSpent("run_resolved", waitBudget),
								"the timeout message itself must use the resolved run ID")
						}
					})
				}
			}
		}
	}
}
