// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"azure.ai.rle/internal/monitor"
	"azure.ai.rle/internal/rollouts"
)

const (
	heliosExperimentPath   = "/experiment_tracking"
	heliosPageSize         = 250
	heliosTracePageLimit   = 20
	heliosContextPageLimit = 20
	heliosMetricLookback   = 5 * time.Minute
)

type heliosJobSource struct {
	rle     *rleClient
	runID   string
	account string

	contextMu         sync.Mutex
	contextLoaded     bool
	nativeContext     bool
	config            json.RawMessage
	status            string
	summaries         []monitor.JobRollout
	summaryByID       map[string]monitor.JobRollout
	traceIndexLoaded  bool
	traceByRollout    map[string]string
	artifactByRollout map[string]string
	traceEntries      map[string]monitor.JobRollout
	discovered        []monitor.JobRollout
}

type heliosPage[T any] struct {
	Data          []T    `json:"data"`
	NextPageToken string `json:"nextPageToken"`
}

type heliosAttribute struct {
	ValueType   string   `json:"valueType"`
	ValueString *string  `json:"valueString"`
	ValueBool   *bool    `json:"valueBool"`
	ValueLong   *int64   `json:"valueLong"`
	ValueDouble *float64 `json:"valueDouble"`
}

type heliosSpan struct {
	TraceID             string                     `json:"traceId"`
	SpanID              string                     `json:"spanId"`
	ParentSpanID        string                     `json:"parentSpanId"`
	SpanName            string                     `json:"spanName"`
	StartTime           time.Time                  `json:"startTime"`
	EndTime             time.Time                  `json:"endTime"`
	DurationNanoseconds int64                      `json:"durationNanoseconds"`
	StatusCode          string                     `json:"statusCode"`
	StatusMessage       string                     `json:"statusMessage"`
	Attributes          map[string]heliosAttribute `json:"attributes"`
}

type heliosMetric struct {
	EventTime     time.Time `json:"eventTime"`
	MetricName    string    `json:"metricName"`
	Value         *float64  `json:"value"`
	ValueLong     *int64    `json:"valueLong"`
	AggregateData struct {
		Sum *float64 `json:"sum"`
	} `json:"aggregateData"`
	Attributes map[string]heliosAttribute `json:"attributes"`
}

type heliosLogRecord struct {
	EventName  string                     `json:"eventName"`
	Attributes map[string]heliosAttribute `json:"attributes"`
}

func newHeliosJobSource(rle *rleClient, runID string) (*heliosJobSource, error) {
	endpoint, err := url.Parse(rle.baseUrl)
	if err != nil || endpoint.Hostname() == "" {
		return nil, errors.New("invalid Helios project endpoint")
	}
	account, _, _ := strings.Cut(endpoint.Hostname(), ".")
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("could not derive the Helios account from the project endpoint")
	}
	return &heliosJobSource{
		rle: rle, runID: runID, account: account,
		summaryByID: map[string]monitor.JobRollout{}, traceByRollout: map[string]string{},
		artifactByRollout: map[string]string{},
		traceEntries:      map[string]monitor.JobRollout{},
	}, nil
}

func (s *heliosJobSource) Config(ctx context.Context) (json.RawMessage, error) {
	if err := s.loadContext(ctx); err != nil {
		return nil, err
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if len(s.config) != 0 {
		return slices.Clone(s.config), nil
	}
	return json.Marshal(map[string]any{"model_name": "Helios telemetry", "max_steps": nil})
}

func (s *heliosJobSource) MetricTailStart(now time.Time) int64 {
	return now.Add(-heliosMetricLookback).UnixMicro()
}

func (s *heliosJobSource) Metrics(
	ctx context.Context, after int64, token string,
) (monitor.JobPage[json.RawMessage], error) {
	if err := s.loadContext(ctx); err != nil {
		return monitor.JobPage[json.RawMessage]{}, err
	}
	s.contextMu.Lock()
	nativeContext := s.nativeContext
	s.contextMu.Unlock()
	var page heliosPage[heliosMetric]
	query := url.Values{}
	if after >= 0 {
		query.Set("from", time.UnixMicro(after).UTC().Format(time.RFC3339Nano))
	}
	if err := s.readPage(ctx, "metrics", token, query, &page, 16<<20); err != nil {
		return monitor.JobPage[json.RawMessage]{}, err
	}
	rows := map[int64]map[string]any{}
	for _, point := range page.Data {
		value, ok := point.number()
		if !ok || strings.TrimSpace(point.MetricName) == "" || point.EventTime.IsZero() {
			continue
		}
		if point.EventTime.UnixMicro() <= after {
			continue
		}
		step := point.EventTime.UnixMicro()
		if nativeContext {
			if point.stringAttribute("rle.telemetry.kind") != "training_metric" {
				continue
			}
			if importedStep, ok := point.integerAttribute("training.step"); ok {
				step = importedStep
			}
		}
		row := rows[step]
		if row == nil {
			row = map[string]any{"step": step, "event_time": point.EventTime}
			rows[step] = row
		}
		row[point.MetricName] = value
		if point.MetricName == "rollout.reward" {
			row["env/all/reward/total"] = value
		}
	}
	result := monitor.JobPage[json.RawMessage]{Next: page.NextPageToken}
	for _, step := range slices.Sorted(maps.Keys(rows)) {
		raw, err := json.Marshal(rows[step])
		if err != nil {
			return monitor.JobPage[json.RawMessage]{}, fmt.Errorf("encode Helios metric row: %w", err)
		}
		result.Data = append(result.Data, raw)
	}
	if result.Data == nil {
		result.Data = []json.RawMessage{}
	}
	return result, nil
}

func (s *heliosJobSource) Rollouts(
	ctx context.Context, token string,
) (monitor.JobPage[monitor.JobRollout], error) {
	if token == "" {
		if err := s.refreshContext(ctx); err != nil {
			return monitor.JobPage[monitor.JobRollout]{}, err
		}
		if err := s.refreshTraceIndex(ctx); err != nil {
			return monitor.JobPage[monitor.JobRollout]{}, err
		}
		s.contextMu.Lock()
		s.rebuildDiscoveriesLocked()
		s.contextMu.Unlock()
	} else if err := s.loadTraceIndex(ctx); err != nil {
		return monitor.JobPage[monitor.JobRollout]{}, err
	}
	s.contextMu.Lock()
	discovered := slices.Clone(s.discovered)
	s.contextMu.Unlock()
	offset := 0
	if token != "" {
		var err error
		offset, err = strconv.Atoi(token)
		if err != nil || offset < 0 || offset > len(discovered) {
			return monitor.JobPage[monitor.JobRollout]{}, errors.New("invalid Helios rollout continuation token")
		}
	}
	end := min(offset+heliosPageSize, len(discovered))
	result := monitor.JobPage[monitor.JobRollout]{Data: slices.Clone(discovered[offset:end])}
	if end < len(discovered) {
		result.Next = strconv.Itoa(end)
	}
	return result, nil
}

func (s *heliosJobSource) rebuildDiscoveriesLocked() {
	entries := maps.Clone(s.traceEntries)
	for rolloutID, summary := range s.summaryByID {
		if trace, ok := entries[rolloutID]; ok {
			entries[rolloutID] = mergeHeliosRollout(summary, trace)
		} else {
			entries[rolloutID] = summary
		}
	}
	s.discovered = slices.Collect(maps.Values(entries))
	slices.SortFunc(s.discovered, func(a, b monitor.JobRollout) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.RolloutID, b.RolloutID))
	})
}

func mergeHeliosRollout(summary, trace monitor.JobRollout) monitor.JobRollout {
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = trace.CreatedAt
	}
	if summary.Status == "" {
		summary.Status = trace.Status
	}
	if summary.Reward == nil {
		summary.Reward = trace.Reward
	}
	if summary.Success == nil {
		summary.Success = trace.Success
	}
	if summary.Latency == nil {
		summary.Latency = trace.Latency
	}
	if summary.SavedAt == nil {
		summary.SavedAt = trace.SavedAt
	}
	summary.HasGraph = summary.HasGraph || trace.HasGraph
	summary.ResultAvailable = summary.ResultAvailable || trace.ResultAvailable
	return summary
}

func (s *heliosJobSource) Detail(ctx context.Context, id string) (monitor.JobRollout, error) {
	traceID, entry, err := s.resolveTrace(ctx, id)
	if err != nil {
		return monitor.JobRollout{}, err
	}
	if traceID == "" {
		return entry, nil
	}
	spans, err := s.readTrace(ctx, traceID)
	if err != nil {
		return monitor.JobRollout{}, err
	}
	return s.rolloutFromSpans(traceID, spans)
}

func (s *heliosJobSource) Result(ctx context.Context, entry monitor.JobRollout) (rollouts.Snapshot, error) {
	traceID, _, err := s.resolveTrace(ctx, entry.RolloutID)
	if err != nil {
		return rollouts.Snapshot{}, err
	}
	s.contextMu.Lock()
	artifactTraceID := s.artifactByRollout[entry.RolloutID]
	s.contextMu.Unlock()
	if artifactTraceID != "" {
		spans, readErr := s.readTrace(ctx, artifactTraceID)
		if readErr != nil {
			return rollouts.Snapshot{}, readErr
		}
		return artifactSnapshotFromSpans(entry.RolloutID, spans)
	}
	if traceID == "" {
		return rollouts.Snapshot{}, errors.New("Helios rollout result is unavailable")
	}
	spans, err := s.readTrace(ctx, traceID)
	if err != nil {
		return rollouts.Snapshot{}, err
	}
	return s.snapshotFromSpans(traceID, spans)
}

func (s *heliosJobSource) Status(ctx context.Context) (string, error) {
	if err := s.loadContext(ctx); err != nil {
		return "", err
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.status != "" {
		return s.status, nil
	}
	return "running", nil
}

func (s *heliosJobSource) loadContext(ctx context.Context) error {
	return s.readContext(ctx, false)
}

func (s *heliosJobSource) refreshContext(ctx context.Context) error {
	return s.readContext(ctx, true)
}

func (s *heliosJobSource) readContext(ctx context.Context, refresh bool) error {
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.contextLoaded && !refresh {
		return nil
	}
	var (
		token     string
		config    json.RawMessage
		status    string
		summaries []monitor.JobRollout
	)
	for range heliosContextPageLimit {
		var page heliosPage[heliosLogRecord]
		if err := s.readPage(ctx, "log-records", token, nil, &page, 16<<20); err != nil {
			if readErr, ok := errors.AsType[*monitor.ReadError](err); ok && readErr.Status == 404 {
				s.contextLoaded = true
				return nil
			}
			return err
		}
		for _, record := range page.Data {
			switch record.stringAttribute("rle.telemetry.kind") {
			case "job_manifest":
				var manifest struct {
					Run struct {
						Config json.RawMessage `json:"config"`
						Meta   struct {
							Status string `json:"status"`
						} `json:"meta"`
					} `json:"run"`
				}
				if err := json.Unmarshal(
					[]byte(record.stringAttribute("rle.job.payload")), &manifest,
				); err != nil {
					return fmt.Errorf("decode Helios job manifest: %w", err)
				}
				config, status = slices.Clone(manifest.Run.Config), strings.TrimSpace(manifest.Run.Meta.Status)
			case "rollout_summary":
				var entry monitor.JobRollout
				if err := json.Unmarshal(
					[]byte(record.stringAttribute("rle.rollout.payload")), &entry,
				); err != nil {
					return fmt.Errorf("decode Helios rollout summary: %w", err)
				}
				if err := rollouts.ValidateID(entry.RolloutID); err != nil {
					return errors.New("Helios rollout summary contains an invalid rollout ID")
				}
				entry.JobID = s.runID
				summaries = append(summaries, entry)
			}
		}
		if page.NextPageToken == "" {
			slices.SortFunc(summaries, func(a, b monitor.JobRollout) int {
				return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.RolloutID, b.RolloutID))
			})
			s.config, s.status, s.summaries = config, status, summaries
			s.summaryByID = make(map[string]monitor.JobRollout, len(summaries))
			for _, entry := range summaries {
				s.summaryByID[entry.RolloutID] = entry
			}
			s.nativeContext = len(config) != 0 || status != "" || len(summaries) != 0
			s.contextLoaded = true
			return nil
		}
		token = page.NextPageToken
	}
	return errors.New("Helios job context exceeded the page safety limit")
}

func (s *heliosJobSource) loadTraceIndex(ctx context.Context) error {
	return s.readTraceIndex(ctx, false)
}

func (s *heliosJobSource) refreshTraceIndex(ctx context.Context) error {
	return s.readTraceIndex(ctx, true)
}

func (s *heliosJobSource) readTraceIndex(ctx context.Context, refresh bool) error {
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.traceIndexLoaded && !refresh {
		return nil
	}
	index := map[string]string{}
	artifacts := map[string]string{}
	artifactEndTimes := map[string]time.Time{}
	entries := map[string]monitor.JobRollout{}
	token := ""
	for range heliosTracePageLimit {
		var page heliosPage[heliosSpan]
		if err := s.readPage(ctx, "traces", token, nil, &page, 16<<20); err != nil {
			return err
		}
		for _, span := range page.Data {
			if isHeliosArtifactRoot(span) {
				rolloutID := span.stringAttribute("rle.rollout.id")
				if rollouts.ValidateID(rolloutID) == nil &&
					isNewerHeliosArtifact(artifacts[rolloutID], artifactEndTimes[rolloutID], span) {
					artifacts[rolloutID] = span.TraceID
					artifactEndTimes[rolloutID] = span.EndTime
				}
				continue
			}
			if !isHeliosRolloutRoot(span) {
				continue
			}
			rolloutID := heliosRolloutID(span)
			if existing := entries[rolloutID]; existing.SavedAt != nil &&
				!span.EndTime.After(*existing.SavedAt) {
				continue
			}
			index[rolloutID] = span.TraceID
			entry, err := s.rolloutFromRoot(span.TraceID, span, false)
			if err != nil {
				return err
			}
			entries[rolloutID] = entry
		}
		if page.NextPageToken == "" {
			s.commitTraceIndexLocked(index, artifacts, entries)
			return nil
		}
		token = page.NextPageToken
	}
	s.commitTraceIndexLocked(index, artifacts, entries)
	return nil
}

func (s *heliosJobSource) commitTraceIndexLocked(
	index map[string]string, artifacts map[string]string, entries map[string]monitor.JobRollout,
) {
	s.traceByRollout = index
	s.artifactByRollout = artifacts
	s.traceEntries = entries
	s.traceIndexLoaded = true
}

func (s *heliosJobSource) resolveTrace(
	ctx context.Context, rolloutID string,
) (string, monitor.JobRollout, error) {
	if err := rollouts.ValidateID(rolloutID); err != nil {
		return "", monitor.JobRollout{}, errors.New("invalid Helios rollout ID")
	}
	if err := s.loadContext(ctx); err != nil {
		return "", monitor.JobRollout{}, err
	}
	if err := s.loadTraceIndex(ctx); err != nil {
		return "", monitor.JobRollout{}, err
	}
	s.contextMu.Lock()
	entry, summarized := s.summaryByID[rolloutID]
	if traceID := s.traceByRollout[rolloutID]; traceID != "" {
		s.contextMu.Unlock()
		return traceID, entry, nil
	}
	if summarized {
		s.contextMu.Unlock()
		return "", entry, nil
	}
	s.contextMu.Unlock()
	if err := s.refreshContext(ctx); err != nil {
		return "", monitor.JobRollout{}, err
	}
	if err := s.refreshTraceIndex(ctx); err != nil {
		return "", monitor.JobRollout{}, err
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	entry, summarized = s.summaryByID[rolloutID]
	if traceID := s.traceByRollout[rolloutID]; traceID != "" {
		return traceID, entry, nil
	}
	if summarized {
		return "", entry, nil
	}
	// Runs exported before native context support exposed trace IDs directly.
	return rolloutID, monitor.JobRollout{}, nil
}

func (s *heliosJobSource) readPage(
	ctx context.Context, collection string, token string, query url.Values, target any, limit int64,
) error {
	if query == nil {
		query = url.Values{}
	} else {
		query = maps.Clone(query)
	}
	query.Set("take", strconv.Itoa(heliosPageSize))
	if token != "" {
		query.Set("pageToken", token)
	}
	path := heliosExperimentPath + "/runs/" + url.PathEscape(s.runID) + "/" + collection + "?" + query.Encode()
	return readMonitorJSON(
		ctx, s.rle.httpClient, s.rle.baseUrl, path, s.rle.authorizationHeader,
		map[string]string{"X-WANDB-USERNAME": s.account}, target, limit,
	)
}

func (s *heliosJobSource) readTrace(ctx context.Context, traceID string) ([]heliosSpan, error) {
	if err := rollouts.ValidateID(traceID); err != nil {
		return nil, errors.New("invalid Helios trace ID")
	}
	var (
		result []heliosSpan
		token  string
		seen   = map[string]bool{}
	)
	for range heliosTracePageLimit {
		var page heliosPage[heliosSpan]
		collection := "traces/" + url.PathEscape(traceID)
		if err := s.readPage(ctx, collection, token, nil, &page, 16<<20); err != nil {
			return nil, err
		}
		result = append(result, page.Data...)
		if page.NextPageToken == "" {
			if len(result) == 0 {
				return nil, errors.New("Helios trace response contained no spans")
			}
			return result, nil
		}
		if seen[page.NextPageToken] {
			return nil, errors.New("Helios repeated a trace continuation token")
		}
		seen[page.NextPageToken] = true
		token = page.NextPageToken
	}
	return nil, errors.New("Helios trace exceeded the page safety limit")
}

func (s *heliosJobSource) rolloutFromSpans(traceID string, spans []heliosSpan) (monitor.JobRollout, error) {
	root, ok := heliosRootSpan(spans)
	if !ok {
		return monitor.JobRollout{}, errors.New("invalid Helios rollout trace")
	}
	entry, err := s.rolloutFromRoot(traceID, root, len(spans) > 1)
	if err != nil {
		return monitor.JobRollout{}, err
	}
	s.contextMu.Lock()
	if summary, ok := s.summaryByID[entry.RolloutID]; ok {
		entry = mergeHeliosRollout(summary, entry)
	}
	s.contextMu.Unlock()
	return entry, nil
}

func (s *heliosJobSource) rolloutFromRoot(
	traceID string, root heliosSpan, hasGraph bool,
) (monitor.JobRollout, error) {
	if !isHeliosRolloutRoot(root) || root.TraceID != traceID || rollouts.ValidateID(traceID) != nil {
		return monitor.JobRollout{}, errors.New("invalid Helios rollout trace")
	}
	rolloutID := heliosRolloutID(root)
	if err := rollouts.ValidateID(rolloutID); err != nil {
		return monitor.JobRollout{}, errors.New("invalid Helios rollout identity")
	}
	entry := monitor.JobRollout{
		RolloutID: rolloutID, JobID: s.runID, CreatedAt: root.StartTime,
		Status:   heliosRolloutStatus(root),
		HasGraph: hasGraph, ResultAvailable: true,
	}
	if reward, ok := root.numberAttribute("rollout.reward"); ok {
		entry.Reward = new(reward)
	}
	if duration, ok := root.numberAttribute("rollout.duration_seconds"); ok {
		entry.Latency = new(duration)
	} else if root.DurationNanoseconds > 0 {
		entry.Latency = new(float64(root.DurationNanoseconds) / float64(time.Second))
	}
	if !root.EndTime.IsZero() {
		entry.SavedAt = new(root.EndTime)
	}
	return entry, nil
}

func (s *heliosJobSource) snapshotFromSpans(traceID string, spans []heliosSpan) (rollouts.Snapshot, error) {
	root, ok := heliosRootSpan(spans)
	if !ok || !isHeliosRolloutRoot(root) || root.TraceID != traceID {
		return rollouts.Snapshot{}, errors.New("Helios trace is missing its root span")
	}
	rolloutID := heliosRolloutID(root)
	reward, ok := root.numberAttribute("rollout.reward")
	if !ok {
		return rollouts.Snapshot{}, errors.New("Helios trace is missing rollout.reward")
	}
	slices.SortFunc(spans, func(a, b heliosSpan) int {
		return cmp.Or(a.StartTime.Compare(b.StartTime), cmp.Compare(a.SpanID, b.SpanID))
	})
	turns := make([]map[string]any, 0)
	steps := make([]map[string]any, 0)
	finalResponse := ""
	for _, span := range spans {
		if span.stringAttribute("gen_ai.operation.name") != "chat" {
			continue
		}
		turn, output, err := heliosTurn(span)
		if err != nil {
			return rollouts.Snapshot{}, err
		}
		turns = append(turns, turn)
		turnIndex, _ := span.integerAttribute("rollout.turn_index")
		toolCount := 0
		toolErrorCount := 0
		for _, tool := range spans {
			if tool.stringAttribute("gen_ai.operation.name") != "execute_tool" {
				continue
			}
			index, ok := tool.integerAttribute("rollout.turn_index")
			if !ok || index != turnIndex {
				continue
			}
			toolCount++
			if strings.EqualFold(tool.StatusCode, "error") {
				toolErrorCount++
			}
		}
		if toolCount > 0 {
			turn["n_tools"] = int64(toolCount)
			turn["n_tool_call_errors"] = int64(toolErrorCount)
		}
		turnReward, _ := span.numberAttribute("rollout.turn_reward")
		steps = append(steps, map[string]any{
			"capture_node_id": turn["node_id"], "reward": turnReward,
			"episode_done": false,
		})
		if output != "" {
			finalResponse = output
		}
	}
	if len(steps) > 0 {
		steps[len(steps)-1]["episode_done"] = true
	}
	response := map[string]any{
		"rollout_id": rolloutID,
		"reward":     reward,
		"episode": map[string]any{
			"kind": "helios", "termination_reason": heliosTerminationReason(root), "steps": steps,
		},
		"rollout_graph": map[string]any{
			"capture_level":     root.stringAttribute("rle.graph.capture_level"),
			"trainable":         root.boolAttribute("rle.graph.trainable"),
			"stats":             heliosGraphStats(root),
			"turns":             turns,
			"sequences":         []any{},
			"validation":        []any{},
			"model_call_errors": []any{},
		},
	}
	if finalResponse != "" {
		response["final_response"] = finalResponse
	}
	if heliosRolloutStatus(root) == "failed" {
		message := strings.TrimSpace(root.StatusMessage)
		if message == "" {
			message = "the Helios root span reported an error"
		}
		response["result"] = map[string]any{
			"agent_response": "ROLLOUT ERROR\nHeliosTraceError: " + message,
		}
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return rollouts.Snapshot{}, fmt.Errorf("encode Helios rollout snapshot: %w", err)
	}
	snapshot := rollouts.Snapshot{
		Response: raw, Source: "Helios experiment tracking",
		Environment: &rollouts.Environment{Name: "Helios", Version: "v1"},
	}
	if !root.EndTime.IsZero() {
		snapshot.SavedAt = root.EndTime
	}
	return snapshot, nil
}

func heliosTurn(span heliosSpan) (map[string]any, string, error) {
	requestMessages, err := heliosMessages(span.stringAttribute("gen_ai.input.messages"))
	if err != nil {
		return nil, "", fmt.Errorf("decode Helios input messages: %w", err)
	}
	outputMessages, err := heliosMessages(span.stringAttribute("gen_ai.output.messages"))
	if err != nil {
		return nil, "", fmt.Errorf("decode Helios output messages: %w", err)
	}
	var responseMessage map[string]any
	finalResponse := ""
	if len(outputMessages) > 0 {
		responseMessage = outputMessages[0]
		if content, ok := responseMessage["content"].(string); ok {
			finalResponse = content
		}
	}
	index, _ := span.integerAttribute("rollout.turn_index")
	prompt, _ := span.integerAttribute("gen_ai.usage.input_tokens")
	sampled, _ := span.integerAttribute("gen_ai.usage.output_tokens")
	tools, _ := span.integerAttribute("rle.capture.tool_count")
	turn := map[string]any{
		"node_id": span.stringAttribute("rle.capture.node_id"),
		"index":   index, "root_id": span.stringAttribute("rle.capture.root_id"),
		"n_prompt": prompt, "n_sampled": sampled, "n_tools": tools,
		"finish_reason":    span.stringAttribute("rle.capture.finish_reason"),
		"discarded":        span.boolAttribute("rle.capture.discarded"),
		"request_messages": requestMessages, "n_tool_call_errors": 0,
		"tool_call_errors": []any{},
	}
	if responseMessage != nil {
		turn["response_message"] = responseMessage
	}
	return turn, finalResponse, nil
}

type heliosWireMessage struct {
	Role  string           `json:"role"`
	Parts []heliosWirePart `json:"parts"`
}

type heliosWirePart struct {
	Type      string          `json:"type"`
	Content   string          `json:"content"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Response  string          `json:"response"`
	Arguments json.RawMessage `json:"arguments"`
}

func heliosMessages(raw string) ([]map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return []map[string]any{}, nil
	}
	var messages []heliosWireMessage
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		var message heliosWireMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			return nil, err
		}
		messages = []heliosWireMessage{message}
	} else if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		converted := map[string]any{"role": message.Role}
		var (
			texts     []string
			toolCalls []map[string]any
		)
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				if part.Content != "" {
					texts = append(texts, part.Content)
				}
			case "tool_call":
				var arguments any
				if len(part.Arguments) > 0 {
					if err := json.Unmarshal(part.Arguments, &arguments); err != nil {
						return nil, err
					}
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": part.ID, "type": "function",
					"function": map[string]any{"name": part.Name, "arguments": arguments},
				})
			case "tool_call_response":
				converted["tool_call_id"] = part.ID
				converted["content"] = part.Response
			}
		}
		if len(texts) > 0 {
			converted["content"] = strings.Join(texts, "\n")
		}
		if len(toolCalls) > 0 {
			converted["tool_calls"] = toolCalls
		}
		result = append(result, converted)
	}
	return result, nil
}

func heliosRootSpan(spans []heliosSpan) (heliosSpan, bool) {
	for _, span := range spans {
		if span.ParentSpanID == "" {
			return span, true
		}
	}
	return heliosSpan{}, false
}

func isNewerHeliosArtifact(existingTraceID string, existingEndTime time.Time, candidate heliosSpan) bool {
	if existingTraceID == "" || candidate.EndTime.After(existingEndTime) {
		return true
	}
	return candidate.EndTime.Equal(existingEndTime) && candidate.TraceID > existingTraceID
}

func isHeliosRolloutRoot(span heliosSpan) bool {
	if span.ParentSpanID != "" || rollouts.ValidateID(span.TraceID) != nil ||
		span.stringAttribute("gen_ai.operation.name") != "invoke_agent" {
		return false
	}
	if _, ok := span.numberAttribute("rollout.reward"); !ok {
		return false
	}
	return span.stringAttribute("rollout.key") != "" || span.stringAttribute("rle.rollout.id") != ""
}

func isHeliosArtifactRoot(span heliosSpan) bool {
	return span.ParentSpanID == "" &&
		span.stringAttribute("rle.telemetry.kind") == "rollout_artifact_manifest" &&
		span.stringAttribute("rle.rollout.id") != "" &&
		span.stringAttribute("rle.result.sha256") != ""
}

func heliosRolloutID(root heliosSpan) string {
	if rolloutID := root.stringAttribute("rle.rollout.id"); rolloutID != "" {
		return rolloutID
	}
	return root.stringAttribute("rollout.key")
}

func heliosRolloutStatus(root heliosSpan) string {
	if strings.EqualFold(root.StatusCode, "error") {
		return "failed"
	}
	if root.EndTime.IsZero() {
		return "running"
	}
	return "completed"
}

func heliosTerminationReason(root heliosSpan) string {
	if reason := strings.TrimSpace(root.stringAttribute("rollout.termination_reason")); reason != "" {
		return reason
	}
	return heliosRolloutStatus(root)
}

func artifactSnapshotFromSpans(rolloutID string, spans []heliosSpan) (rollouts.Snapshot, error) {
	var manifest *heliosSpan
	for i := range spans {
		if isHeliosArtifactRoot(spans[i]) &&
			spans[i].stringAttribute("rle.rollout.id") == rolloutID {
			manifest = &spans[i]
			break
		}
	}
	if manifest == nil {
		return rollouts.Snapshot{}, errors.New("Helios artifact trace is missing its manifest")
	}
	if manifest.stringAttribute("rle.result.encoding") != "gzip+base64" {
		return rollouts.Snapshot{}, errors.New("Helios artifact uses an unsupported encoding")
	}
	count, ok := manifest.integerAttribute("rle.result.chunk.count")
	if !ok || count <= 0 || count > 10000 {
		return rollouts.Snapshot{}, errors.New("Helios artifact has an invalid chunk count")
	}
	expectedSize, ok := manifest.integerAttribute("rle.result.uncompressed_bytes")
	if !ok || expectedSize < 0 || expectedSize > 64<<20 {
		return rollouts.Snapshot{}, errors.New("Helios artifact exceeds the supported size")
	}
	chunks := make([][]byte, int(count))
	for _, span := range spans {
		if span.stringAttribute("rle.telemetry.kind") != "rollout_artifact_chunk" ||
			span.stringAttribute("rle.rollout.id") != rolloutID {
			continue
		}
		index, valid := span.integerAttribute("rle.result.chunk.index")
		if !valid || index < 0 || index >= count {
			return rollouts.Snapshot{}, errors.New("Helios artifact contains an invalid chunk index")
		}
		data, err := base64.StdEncoding.DecodeString(span.stringAttribute("rle.result.chunk.data"))
		if err != nil {
			return rollouts.Snapshot{}, errors.New("Helios artifact contains invalid base64")
		}
		if chunks[index] != nil && !bytes.Equal(chunks[index], data) {
			return rollouts.Snapshot{}, errors.New("Helios artifact contains conflicting duplicate chunks")
		}
		chunks[index] = data
	}
	var compressed bytes.Buffer
	for _, chunk := range chunks {
		if chunk == nil {
			return rollouts.Snapshot{}, errors.New("Helios artifact is missing a chunk")
		}
		compressed.Write(chunk)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		return rollouts.Snapshot{}, errors.New("Helios artifact gzip header is invalid")
	}
	payload, err := io.ReadAll(io.LimitReader(reader, expectedSize+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || int64(len(payload)) != expectedSize {
		return rollouts.Snapshot{}, errors.New("Helios artifact could not be decompressed completely")
	}
	digest := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.stringAttribute("rle.result.sha256")) {
		return rollouts.Snapshot{}, errors.New("Helios artifact checksum verification failed")
	}
	var response struct {
		RolloutID string `json:"rollout_id"`
	}
	if err := json.Unmarshal(payload, &response); err != nil || response.RolloutID != rolloutID {
		return rollouts.Snapshot{}, errors.New("Helios artifact does not contain the requested rollout")
	}
	snapshot := rollouts.Snapshot{
		Response: payload, Source: "Helios verified rollout artifact",
		Environment: &rollouts.Environment{Name: "Helios", Version: "v1"},
	}
	if !manifest.EndTime.IsZero() {
		snapshot.SavedAt = manifest.EndTime
	}
	return snapshot, nil
}

func heliosGraphStats(root heliosSpan) map[string]any {
	result := map[string]any{}
	for _, name := range []string{
		"n_turns", "n_roots", "n_forks", "n_discarded", "n_sequences",
		"n_trainable_sequences", "n_trainable_tokens", "n_model_call_errors",
	} {
		if value, ok := root.integerAttribute("rle.graph.stats." + name); ok {
			result[name] = value
		}
	}
	return result
}

func (m heliosMetric) number() (float64, bool) {
	if m.Value != nil {
		return *m.Value, true
	}
	if m.ValueLong != nil {
		return float64(*m.ValueLong), true
	}
	if m.AggregateData.Sum != nil {
		return *m.AggregateData.Sum, true
	}
	return 0, false
}

func (m heliosMetric) stringAttribute(name string) string {
	return heliosStringAttribute(m.Attributes, name)
}

func (m heliosMetric) integerAttribute(name string) (int64, bool) {
	return heliosIntegerAttribute(m.Attributes, name)
}

func (r heliosLogRecord) stringAttribute(name string) string {
	return heliosStringAttribute(r.Attributes, name)
}

func (s heliosSpan) stringAttribute(name string) string {
	return heliosStringAttribute(s.Attributes, name)
}

func (s heliosSpan) numberAttribute(name string) (float64, bool) {
	value, ok := s.Attributes[name]
	if !ok {
		return 0, false
	}
	if value.ValueDouble != nil {
		return *value.ValueDouble, true
	}
	if value.ValueLong != nil {
		return float64(*value.ValueLong), true
	}
	return 0, false
}

func (s heliosSpan) integerAttribute(name string) (int64, bool) {
	return heliosIntegerAttribute(s.Attributes, name)
}

func heliosStringAttribute(attributes map[string]heliosAttribute, name string) string {
	value, ok := attributes[name]
	if !ok || value.ValueString == nil {
		return ""
	}
	return *value.ValueString
}

func heliosIntegerAttribute(attributes map[string]heliosAttribute, name string) (int64, bool) {
	value, ok := attributes[name]
	if !ok || value.ValueLong == nil {
		return 0, false
	}
	return *value.ValueLong, true
}

func (s heliosSpan) boolAttribute(name string) bool {
	value, ok := s.Attributes[name]
	return ok && value.ValueBool != nil && *value.ValueBool
}
