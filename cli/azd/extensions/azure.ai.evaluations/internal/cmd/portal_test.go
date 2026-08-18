// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The portal link is the last line of a detail view, and it is the one thing a
// user clicks to see the run they just waited for.
func TestWritePortalLink(t *testing.T) {
	var buf bytes.Buffer
	writePortalLink(&buf, "https://ai.azure.com/nextgen/r/x,y,,z,p/build/evaluations/e/run/r")

	out := buf.String()
	assert.Contains(t, out, "Portal: ")
	assert.Contains(t, out, "/build/evaluations/e/run/r")
	assert.True(t, strings.HasSuffix(out, "\n"), "it closes the view, so it ends the line")
}

// Resolution is best effort: the link is a convenience on top of work already
// done, so having none must print nothing rather than an empty label that
// reads like a failure.
func TestWritePortalLink_SilentWithoutAURL(t *testing.T) {
	var buf bytes.Buffer
	writePortalLink(&buf, "")

	assert.Empty(t, buf.String())
}

// `-o json` carries the same link the terminal prints, so a pipeline reading
// JSON is not the one consumer that cannot find the run in the portal.
func TestRunPortalURLTravelsInJSON(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:        "evalrun_1",
		Status:    "completed",
		PortalURL: "https://ai.azure.com/nextgen/r/x,y,,z,p/build/evaluations/eval_1/run/evalrun_1",
	}

	var buf bytes.Buffer
	require.NoError(t, emitJSON(&buf, run))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	assert.Equal(t, run.PortalURL, decoded["portal_url"],
		"the key is portal_url, which is what the spec tells consumers to read")
}

// A run with no portal link must not carry an empty key, or a consumer cannot
// tell "no link" from "link is the empty string".
func TestRunWithoutPortalURLOmitsTheKey(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitJSON(&buf, &eval_api.OpenAIEvalRun{ID: "evalrun_1"}))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	assert.NotContains(t, decoded, "portal_url")
}

// The portal URL is built from the eval and run ids, which is what makes the
// link land on the run rather than the eval's list of them.
func TestPortalRunURLShape(t *testing.T) {
	prefix, err := eval_api.NewPortalPrefix(
		"/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(
		prefix.EvalRunURL("eval_1", "evalrun_9"),
		"/build/evaluations/eval_1/run/evalrun_9"))
}

// A run has one destination. The service's report_url and the portal URL the
// extension builds resolve to the same page, and printing both put two labels
// on it with no rule a reader could infer.
func TestRenderRunPrintsOneLink(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:        "evalrun_1",
		Status:    "completed",
		ReportURL: "https://service.example/report/1",
		PortalURL: "https://ai.azure.com/nextgen/r/x,y,,z,p/build/evaluations/e/run/r",
	}

	var buf bytes.Buffer
	require.NoError(t, renderRun(&buf, run, nil))

	out := buf.String()
	assert.Contains(t, out, "Report: https://service.example/report/1",
		"the service's url wins where it sent one")
	assert.NotContains(t, out, "Portal: ",
		"the second label named the same destination")
	assert.NotContains(t, out, run.PortalURL)
}

// Ours is the fallback, so a service that sends no report_url does not leave
// the reader with no way to open the run.
func TestRenderRunFallsBackToTheBuiltLink(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:        "evalrun_1",
		Status:    "completed",
		PortalURL: "https://ai.azure.com/nextgen/r/x,y,,z,p/build/evaluations/e/run/r",
	}

	var buf bytes.Buffer
	require.NoError(t, renderRun(&buf, run, nil))

	assert.Contains(t, buf.String(), "Report: "+run.PortalURL)
}

// A run with neither prints no link rather than an empty label.
func TestRenderRunOmitsAnAbsentLink(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderRun(&buf, &eval_api.OpenAIEvalRun{
		ID: "evalrun_1", Status: "completed",
	}, nil))

	assert.NotContains(t, buf.String(), "Report:")
}
