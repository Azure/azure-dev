// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/monitor"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	heliosMonitorRunID   = "rle-helios-reviewed-20261014T192404Z"
	heliosMonitorTraceID = "ee5f85a56f6dc5a61c52b0c815b827a4"
)

func TestHeliosMonitorCommandIsSeparateAndVisible(t *testing.T) {
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	oldCreate, oldRun := createRleClient, runHeliosJobMonitor
	t.Cleanup(func() { createRleClient, runHeliosJobMonitor = oldCreate, oldRun })
	createRleClient = func(endpoint string) (*rleClient, error) {
		return newRleClientWithCredential(endpoint, &testTokenCredential{}), nil
	}
	called := false
	runHeliosJobMonitor = func(
		_ context.Context, source monitor.JobSource, runID string, noBrowser bool, _, _ io.Writer,
	) error {
		called = true
		if _, ok := source.(*heliosJobSource); !ok || runID != heliosMonitorRunID || !noBrowser {
			t.Fatalf("unexpected Helios monitor invocation: source=%T run=%q noBrowser=%t", source, runID, noBrowser)
		}
		return nil
	}
	command := newHeliosMonitorCommand()
	command.SetArgs([]string{"--run-id", heliosMonitorRunID, "--no-browser"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Helios monitor was not started")
	}

	root := NewRootCommand()
	heliosCommand, _, err := root.Find([]string{"helios-monitor"})
	if err != nil {
		t.Fatal(err)
	}
	if heliosCommand.Hidden || !strings.Contains(heliosCommand.Short, "Helios Monitor Dashboard") {
		t.Fatalf("Helios command is not clearly visible: hidden=%t short=%q", heliosCommand.Hidden, heliosCommand.Short)
	}
	standardCommand, _, err := root.Find([]string{"monitor"})
	if err != nil {
		t.Fatal(err)
	}
	if standardCommand.Use != "monitor (--rollout-id <id> | --job-id <id>)" {
		t.Fatalf("existing monitor command changed: %q", standardCommand.Use)
	}
}

func TestHeliosMonitorValidation(t *testing.T) {
	for _, tc := range []struct {
		name, runID, code string
	}{
		{"missing", "", "rle_helios_run_id_required"},
		{"path separator", "run/other", "rle_invalid_helios_run_id"},
		{"control character", "run\nother", "rle_invalid_helios_run_id"},
		{"too long", strings.Repeat("x", 257), "rle_invalid_helios_run_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newHeliosMonitorCommand()
			command.SetArgs([]string{"--run-id", tc.runID})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			err := command.Execute()
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != tc.code || localErr.Suggestion == "" {
				t.Fatalf("unexpected validation error: %#v", err)
			}
		})
	}
}

func TestHeliosJobSourceMapsLiveContract(t *testing.T) {
	start := time.Date(2026, 10, 14, 19, 24, 8, 240879200, time.UTC)
	end := start.Add(341300 * time.Nanosecond)
	root := heliosTestSpan(heliosMonitorTraceID, "root", "", "invoke_agent rle", start, end, map[string]any{
		"gen_ai.operation.name":              "invoke_agent",
		"rollout.key":                        "c6e56f7d00a349deba381829511501a8",
		"rollout.reward":                     1.0,
		"rollout.duration_seconds":           91.453,
		"rle.graph.capture_level":            "tokens",
		"rle.graph.trainable":                true,
		"rle.graph.stats.n_turns":            int64(2),
		"rle.graph.stats.n_roots":            int64(1),
		"rle.graph.stats.n_sequences":        int64(1),
		"rle.graph.stats.n_trainable_tokens": int64(27),
	})
	firstChat := heliosTestSpan(heliosMonitorTraceID, "chat1", "root", "chat", start.Add(time.Microsecond),
		start.Add(2*time.Microsecond), map[string]any{
			"gen_ai.operation.name": "chat",
			"gen_ai.input.messages": `[{"role":"user","parts":[{"type":"text","content":"Add 2 + 3"}]}]`,
			"gen_ai.output.messages": `[{"role":"assistant","parts":[{"type":"tool_call","id":"call_0",` +
				`"name":"add","arguments":{"left":2,"right":3}}]}]`,
			"gen_ai.usage.input_tokens":  int64(193),
			"gen_ai.usage.output_tokens": int64(25),
			"rle.capture.node_id":        "node1",
			"rle.capture.root_id":        "node1",
			"rle.capture.finish_reason":  "tool_calls",
			"rle.capture.discarded":      false,
			"rle.capture.tool_count":     int64(1),
			"rollout.turn_index":         int64(0),
			"rollout.turn_reward":        0.0,
		})
	secondChat := heliosTestSpan(heliosMonitorTraceID, "chat2", "root", "chat", start.Add(3*time.Microsecond),
		start.Add(4*time.Microsecond), map[string]any{
			"gen_ai.operation.name":      "chat",
			"gen_ai.input.messages":      `[{"role":"tool","parts":[{"type":"tool_call_response","id":"call_0","response":"5"}]}]`,
			"gen_ai.output.messages":     `[{"role":"assistant","parts":[{"type":"text","content":"5"}]}]`,
			"gen_ai.usage.input_tokens":  int64(649),
			"gen_ai.usage.output_tokens": int64(2),
			"rle.capture.node_id":        "node2",
			"rle.capture.root_id":        "node1",
			"rle.capture.finish_reason":  "stop",
			"rle.capture.discarded":      false,
			"rle.capture.tool_count":     int64(1),
			"rollout.turn_index":         int64(1),
			"rollout.turn_reward":        1.0,
		})
	unrelated := heliosTestSpan("11111111111111111111111111111111", "other", "", "unrelated", start, end,
		map[string]any{"gen_ai.operation.name": "unrelated"})

	var requests []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-WANDB-USERNAME") == "" {
			t.Error("Helios request is missing Foundry authentication or routing headers")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
				heliosTestMetric(start, "rollout.reward", 1.0),
				heliosTestMetric(start, "rollout.response_tokens", int64(27)),
			}})
		case strings.HasSuffix(r.URL.Path, "/traces/"+heliosMonitorTraceID):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{root, firstChat, secondChat}})
		case strings.HasSuffix(r.URL.Path, "/traces"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{root, firstChat, unrelated}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	credential := &testTokenCredential{}
	rle := newRleClientWithCredential(server.URL+"/api/projects/project", credential)
	rle.httpClient = server.Client()
	source, err := newHeliosJobSource(rle, heliosMonitorRunID)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := source.Metrics(t.Context(), -1, "")
	if err != nil || len(metrics.Data) != 1 || metrics.Next != "" {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	var metricRow map[string]any
	if err := json.Unmarshal(metrics.Data[0], &metricRow); err != nil {
		t.Fatal(err)
	}
	if metricRow["env/all/reward/total"] != 1.0 || metricRow["rollout.response_tokens"] != float64(27) {
		t.Fatalf("unexpected mapped metrics: %#v", metricRow)
	}
	if _, err := source.Metrics(t.Context(), start.UnixMicro(), ""); err != nil {
		t.Fatal(err)
	}

	entries, err := source.Rollouts(t.Context(), "")
	if err != nil || len(entries.Data) != 1 {
		t.Fatalf("rollouts=%+v err=%v", entries, err)
	}
	entry := entries.Data[0]
	const rolloutID = "c6e56f7d00a349deba381829511501a8"
	if entry.RolloutID != rolloutID || entry.JobID != heliosMonitorRunID ||
		entry.Status != "completed" || entry.Reward == nil || *entry.Reward != 1 {
		t.Fatalf("unexpected rollout mapping: %+v", entry)
	}

	detail, err := source.Detail(t.Context(), rolloutID)
	if err != nil || !detail.HasGraph || !detail.ResultAvailable {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	snapshot, err := source.Result(t.Context(), detail)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		RolloutID     string `json:"rollout_id"`
		Reward        float64
		FinalResponse string `json:"final_response"`
		RolloutGraph  struct {
			Turns []struct {
				ResponseMessage struct {
					Content   string `json:"content"`
					ToolCalls []any  `json:"tool_calls"`
				} `json:"response_message"`
			} `json:"turns"`
		} `json:"rollout_graph"`
	}
	if err := json.Unmarshal(snapshot.Response, &response); err != nil {
		t.Fatal(err)
	}
	if response.RolloutID != rolloutID || response.Reward != 1 ||
		response.FinalResponse != "5" || len(response.RolloutGraph.Turns) != 2 ||
		len(response.RolloutGraph.Turns[0].ResponseMessage.ToolCalls) != 1 {
		t.Fatalf("unexpected snapshot: %s", snapshot.Response)
	}
	if status, err := source.Status(t.Context()); err != nil || status != "running" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	for _, request := range requests {
		if !strings.Contains(request, "/experiment_tracking/runs/"+heliosMonitorRunID+"/") ||
			!strings.Contains(request, "take=250") {
			t.Fatalf("unexpected Helios request: %s", request)
		}
	}
	if !slices.ContainsFunc(requests, func(request string) bool {
		return strings.Contains(request, "/metrics?") && strings.Contains(request, "from=")
	}) {
		t.Fatalf("incremental metric request did not include a time lower bound: %v", requests)
	}
	if len(credential.scopes) == 0 || credential.scopes[0] != foundryTokenScope {
		t.Fatalf("unexpected credential scopes: %v", credential.scopes)
	}
}

func TestHeliosFailedTraceMapsToDashboardExecutionFailure(t *testing.T) {
	start := time.Date(2026, 10, 14, 19, 24, 8, 0, time.UTC)
	rootMap := heliosTestSpan(heliosMonitorTraceID, "root", "", "invoke_agent rle", start, start.Add(time.Second),
		map[string]any{
			"gen_ai.operation.name":   "invoke_agent",
			"rollout.key":             "c6e56f7d00a349deba381829511501a8",
			"rollout.reward":          0.0,
			"rle.graph.capture_level": "messages",
		})
	rootMap["statusCode"] = "error"
	rootMap["statusMessage"] = "tool execution failed"
	raw, err := json.Marshal(rootMap)
	if err != nil {
		t.Fatal(err)
	}

	var root heliosSpan
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	source := &heliosJobSource{runID: heliosMonitorRunID}
	snapshot, err := source.snapshotFromSpans(heliosMonitorTraceID, []heliosSpan{root})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(snapshot.Response, []byte(`"agent_response":"ROLLOUT ERROR\nHeliosTraceError: tool execution failed"`)) {
		t.Fatalf("failed trace did not map to the dashboard execution-failure contract: %s", snapshot.Response)
	}
}

func TestHeliosVerifiedArtifactRestoresExactRolloutResponse(t *testing.T) {
	const rolloutID = "c6e56f7d00a349deba381829511501a8"
	payload := []byte(`{"rollout_id":"` + rolloutID +
		`","reward":1,"rollout_graph":{"turns":[{"index":0,"response_message":{"role":"assistant","content":"5"}}]}}`)
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	start := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	traceID := "1234567890abcdef1234567890abcdef"
	manifestMap := heliosTestSpan(traceID, "manifest", "", "rle.result.manifest", start, start, map[string]any{
		"rle.telemetry.kind":            "rollout_artifact_manifest",
		"rle.rollout.id":                rolloutID,
		"rle.result.sha256":             hash,
		"rle.result.encoding":           "gzip+base64",
		"rle.result.chunk.count":        int64(1),
		"rle.result.uncompressed_bytes": int64(len(payload)),
		"rle.result.compressed_bytes":   int64(compressed.Len()),
	})
	chunkMap := heliosTestSpan(traceID, "chunk", "manifest", "rle.result.chunk", start, start, map[string]any{
		"rle.telemetry.kind":     "rollout_artifact_chunk",
		"rle.rollout.id":         rolloutID,
		"rle.result.sha256":      hash,
		"rle.result.chunk.index": int64(0),
		"rle.result.chunk.count": int64(1),
		"rle.result.chunk.data":  base64.StdEncoding.EncodeToString(compressed.Bytes()),
	})
	var manifest, chunk heliosSpan
	for source, target := range map[*map[string]any]*heliosSpan{
		&manifestMap: &manifest,
		&chunkMap:    &chunk,
	} {
		raw, err := json.Marshal(*source)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := artifactSnapshotFromSpans(rolloutID, []heliosSpan{manifest, chunk})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot.Response, payload) ||
		snapshot.Source != "Helios verified rollout artifact" {
		t.Fatalf("artifact was not restored exactly: source=%q payload=%s", snapshot.Source, snapshot.Response)
	}

	corruptHash := strings.Repeat("0", 64)
	manifest.Attributes["rle.result.sha256"] = heliosAttribute{
		ValueType: "string", ValueString: &corruptHash,
	}
	if _, err := artifactSnapshotFromSpans(rolloutID, []heliosSpan{manifest, chunk}); err == nil ||
		!strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt artifact was not rejected: %v", err)
	}
}

func TestHeliosNativeMetricsFilterByEventTimeAndKeepTrainingStep(t *testing.T) {
	eventTime := time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/log-records"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{
				"eventName": "rle.job.manifest",
				"attributes": map[string]any{
					"rle.telemetry.kind": heliosTestAttribute("job_manifest"),
					"rle.job.payload": heliosTestAttribute(
						`{"run":{"config":{"max_steps":10},"meta":{"status":"running"}}}`,
					),
				},
			}}})
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			metric := heliosTestMetric(eventTime, "loss", 1.25)
			delete(metric, "value")
			metric["aggregateData"] = map[string]any{"sum": 1.25}
			metric["attributes"] = map[string]any{
				"rle.telemetry.kind": heliosTestAttribute("training_metric"),
				"training.step":      heliosTestAttribute(int64(7)),
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{metric}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rle := newRleClientWithCredential(server.URL+"/api/projects/project", &testTokenCredential{})
	rle.httpClient = server.Client()
	source, err := newHeliosJobSource(rle, heliosMonitorRunID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Metrics(t.Context(), eventTime.Add(-time.Microsecond).UnixMicro(), "")
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("metrics=%+v err=%v", page, err)
	}
	var row struct {
		Step      int64     `json:"step"`
		EventTime time.Time `json:"event_time"`
		Loss      float64   `json:"loss"`
	}
	if err := json.Unmarshal(page.Data[0], &row); err != nil {
		t.Fatal(err)
	}
	if row.Step != 7 || !row.EventTime.Equal(eventTime) || row.Loss != 1.25 {
		t.Fatalf("native metric lost its training step or event time: %+v", row)
	}
	page, err = source.Metrics(t.Context(), eventTime.UnixMicro(), "")
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("metric tail did not filter by event time: %+v err=%v", page, err)
	}
}

func TestHeliosRolloutsMergeSummariesAndRefreshDiscovery(t *testing.T) {
	start := time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)
	const (
		summaryID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		firstID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		laterID   = "cccccccccccccccccccccccccccccccc"
	)
	phase := 0
	summary := monitor.JobRollout{
		RolloutID: summaryID, Status: "failed", CreatedAt: start, ResultAvailable: false,
	}
	laterSummary := monitor.JobRollout{
		RolloutID: laterID, Status: "completed", CreatedAt: start.Add(2 * time.Second),
	}
	firstRoot := heliosTestSpan(firstID, "root-b", "", "invoke_agent rle", start.Add(time.Second),
		start.Add(1500*time.Millisecond), map[string]any{
			"gen_ai.operation.name": "invoke_agent",
			"rollout.key":           firstID,
			"rollout.reward":        1.0,
		})
	laterRoot := heliosTestSpan(laterID, "root-c", "", "invoke_agent rle", start.Add(2*time.Second),
		start.Add(2500*time.Millisecond), map[string]any{
			"gen_ai.operation.name": "invoke_agent",
			"rollout.key":           laterID,
			"rollout.reward":        0.5,
		})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/log-records"):
			records := []any{heliosTestSummaryRecord(t, summary)}
			if phase > 0 {
				records = append(records, heliosTestSummaryRecord(t, laterSummary))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": records})
		case strings.HasSuffix(r.URL.Path, "/traces/"+laterID):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{laterRoot}})
		case strings.HasSuffix(r.URL.Path, "/traces"):
			traces := []any{firstRoot}
			if phase > 0 {
				traces = append(traces, laterRoot)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": traces})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rle := newRleClientWithCredential(server.URL+"/api/projects/project", &testTokenCredential{})
	rle.httpClient = server.Client()
	source, err := newHeliosJobSource(rle, heliosMonitorRunID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Rollouts(t.Context(), "")
	if err != nil || len(page.Data) != 2 {
		t.Fatalf("initial rollouts=%+v err=%v", page, err)
	}
	if !slices.ContainsFunc(page.Data, func(entry monitor.JobRollout) bool {
		return entry.RolloutID == summaryID && !entry.ResultAvailable
	}) || !slices.ContainsFunc(page.Data, func(entry monitor.JobRollout) bool {
		return entry.RolloutID == firstID && entry.ResultAvailable
	}) {
		t.Fatalf("summary and trace rollouts were not merged: %+v", page.Data)
	}

	phase = 1
	page, err = source.Rollouts(t.Context(), "")
	if err != nil || len(page.Data) != 3 {
		t.Fatalf("refreshed rollouts=%+v err=%v", page, err)
	}
	later, err := source.Detail(t.Context(), laterID)
	if err != nil || later.RolloutID != laterID || !later.ResultAvailable {
		t.Fatalf("later trace was not resolvable: %+v err=%v", later, err)
	}
}

func TestHeliosRolloutsExposeBoundedTraceIndexWhenMorePagesRemain(t *testing.T) {
	start := time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/log-records"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case strings.HasSuffix(r.URL.Path, "/traces"):
			requests++
			rolloutID := fmt.Sprintf("%032x", requests)
			root := heliosTestSpan(
				rolloutID,
				fmt.Sprintf("root-%d", requests),
				"",
				"invoke_agent rle",
				start.Add(time.Duration(requests)*time.Second),
				start.Add(time.Duration(requests)*time.Second+time.Millisecond),
				map[string]any{
					"gen_ai.operation.name": "invoke_agent",
					"rollout.key":           rolloutID,
					"rollout.reward":        1.0,
				},
			)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":          []any{root},
				"nextPageToken": fmt.Sprintf("page-%d", requests+1),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rle := newRleClientWithCredential(server.URL+"/api/projects/project", &testTokenCredential{})
	rle.httpClient = server.Client()
	source, err := newHeliosJobSource(rle, heliosMonitorRunID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Rollouts(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if requests != heliosTracePageLimit || len(page.Data) != heliosTracePageLimit {
		t.Fatalf("requests=%d rollouts=%d", requests, len(page.Data))
	}
}

func heliosTestSummaryRecord(t *testing.T, entry monitor.JobRollout) map[string]any {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"eventName": "rle.rollout.summary",
		"attributes": map[string]any{
			"rle.telemetry.kind":  heliosTestAttribute("rollout_summary"),
			"rle.rollout.payload": heliosTestAttribute(string(raw)),
		},
	}
}

func TestHeliosArtifactIndexSelectsLatestDeterministically(t *testing.T) {
	end := time.Date(2026, 10, 14, 19, 24, 8, 0, time.UTC)
	older := heliosSpan{TraceID: "11111111111111111111111111111111", EndTime: end}
	newer := heliosSpan{TraceID: "22222222222222222222222222222222", EndTime: end.Add(time.Second)}
	tiedLower := heliosSpan{TraceID: "00000000000000000000000000000000", EndTime: newer.EndTime}
	tiedHigher := heliosSpan{TraceID: "33333333333333333333333333333333", EndTime: newer.EndTime}

	if !isNewerHeliosArtifact("", time.Time{}, older) {
		t.Fatal("first artifact was not selected")
	}
	if !isNewerHeliosArtifact(older.TraceID, older.EndTime, newer) {
		t.Fatal("newer artifact was not selected")
	}
	if isNewerHeliosArtifact(newer.TraceID, newer.EndTime, tiedLower) {
		t.Fatal("lower trace ID unexpectedly replaced an equally recent artifact")
	}
	if !isNewerHeliosArtifact(newer.TraceID, newer.EndTime, tiedHigher) {
		t.Fatal("higher trace ID did not deterministically replace an equally recent artifact")
	}
}

func heliosTestSpan(
	traceID string,
	spanID string,
	parentID string,
	name string,
	start time.Time,
	end time.Time,
	attributes map[string]any,
) map[string]any {
	encodedAttributes := map[string]any{}
	for key, value := range attributes {
		encodedAttributes[key] = heliosTestAttribute(value)
	}
	return map[string]any{
		"traceId": traceID, "spanId": spanID, "parentSpanId": parentID, "spanName": name,
		"startTime": start, "endTime": end, "durationNanoseconds": end.Sub(start).Nanoseconds(),
		"statusCode": "ok", "attributes": encodedAttributes,
	}
}

func heliosTestMetric(eventTime time.Time, name string, value any) map[string]any {
	result := map[string]any{"eventTime": eventTime, "metricName": name, "attributes": map[string]any{}}
	switch typed := value.(type) {
	case int64:
		result["valueLong"] = typed
	case float64:
		result["value"] = typed
	}
	return result
}

func heliosTestAttribute(value any) map[string]any {
	switch typed := value.(type) {
	case string:
		return map[string]any{"valueType": "string", "valueString": typed}
	case bool:
		return map[string]any{"valueType": "bool", "valueBool": typed}
	case int64:
		return map[string]any{"valueType": "long", "valueLong": typed}
	case float64:
		return map[string]any{"valueType": "double", "valueDouble": typed}
	default:
		panic("unsupported test attribute")
	}
}

func TestHeliosMonitorRejectsOutputFormat(t *testing.T) {
	command := newHeliosMonitorCommand()
	command.Flags().String("output", "", "output")
	command.SetArgs([]string{"--run-id", heliosMonitorRunID, "--output", "json"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != "rle_monitor_conflicting_arguments" {
		t.Fatalf("unexpected output validation error: %v", err)
	}
}

func TestHeliosMonitorLive(t *testing.T) {
	projectEndpoint := strings.TrimSpace(os.Getenv("RLE_HELIOS_LIVE_PROJECT_ENDPOINT"))
	runID := strings.TrimSpace(os.Getenv("RLE_HELIOS_LIVE_RUN_ID"))
	if projectEndpoint == "" || runID == "" {
		t.Skip("set RLE_HELIOS_LIVE_PROJECT_ENDPOINT and RLE_HELIOS_LIVE_RUN_ID to run the live smoke")
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	client.credential = newCachedTokenCredential(client.credential)
	source, err := newHeliosJobSource(client, runID)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	outputReader, outputWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- monitor.RunHeliosJob(ctx, source, runID, true, outputWriter, io.Discard)
		_ = outputWriter.Close()
	}()
	line, err := bufio.NewReader(outputReader).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	prefix := "Helios monitor dashboard for " + runID + ": "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("unexpected live monitor heading: %q", line)
	}
	baseURL := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	body := liveMonitorGet(t, baseURL)
	if !bytes.Contains(body, []byte("<title>Foundry RLE</title>")) {
		t.Fatal("live monitor did not serve the existing embedded dashboard")
	}

	var rolloutsResponse struct {
		Data []monitor.JobRollout `json:"data"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		body = liveMonitorGet(t, baseURL+"api/rollouts")
		if err := json.Unmarshal(body, &rolloutsResponse); err != nil {
			t.Fatal(err)
		}
		if len(rolloutsResponse.Data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live Helios monitor did not discover a rollout")
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, path := range []string{"api/run", "api/run/metrics"} {
		if body := liveMonitorGet(t, baseURL+path); !json.Valid(body) {
			t.Fatalf("%s returned invalid JSON", path)
		}
	}
	selected := rolloutsResponse.Data[len(rolloutsResponse.Data)-1]
	body = liveMonitorGet(t, baseURL+"api/rollout?id="+selected.RolloutID)
	var snapshot struct {
		Source   string          `json:"source"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Source != "Helios experiment tracking" || !json.Valid(snapshot.Response) {
		t.Fatalf("unexpected live Helios snapshot: %s", body)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func liveMonitorGet(t *testing.T, endpoint string) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned HTTP %d: %s", endpoint, response.StatusCode, body)
	}
	return body
}
