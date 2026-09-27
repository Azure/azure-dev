// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"azure.ai.rle/internal/rollouts"
)

func TestClassifyExecutionReportsCompletionForAGradedRollout(t *testing.T) {
	state := classifyExecution(json.RawMessage(
		`{"result":{"agent_response":"The competitor raised a Series B."},"rollout":{"turns":[{},{}]}}`))
	if state.State != executionCompleted {
		t.Fatalf("state = %q, want %q", state.State, executionCompleted)
	}
	if state.Error != "" {
		t.Fatalf("error = %q, want empty", state.Error)
	}
}

// A crash is invisible in the index -- it carries a reward, a graph and
// success=false like any poor attempt -- so the traceback in the body is the
// only thing that can report it.
func TestClassifyExecutionReportsTheExceptionThatStoppedTheRollout(t *testing.T) {
	traceback := "ROLLOUT ERROR\nTraceback (most recent call last):\n" +
		"  File \"pipeline.py\", line 622, in run\n" +
		"    raise\n" +
		"httpx.ConnectError: connection refused\n" +
		"\nDuring handling of the above exception, another exception occurred:\n" +
		"  File \"pipeline.py\", line 330, in _create_with_retry\n" +
		"openai.APITimeoutError: Request timed out."
	state := classifyExecution(json.RawMessage(
		`{"result":{"agent_response":` + mustQuote(traceback) + `},"rollout":{"turns":[{}]}}`))
	if state.State != executionFailed {
		t.Fatalf("state = %q, want %q", state.State, executionFailed)
	}
	// The last exception is the one that escaped; the earlier one is a frame it
	// wrapped, so reporting the first would name the wrong cause.
	if state.Error != "openai.APITimeoutError" {
		t.Fatalf("error = %q, want openai.APITimeoutError", state.Error)
	}
	if state.Detail != "Request timed out." {
		t.Fatalf("detail = %q, want %q", state.Detail, "Request timed out.")
	}
}

func TestClassifyExecutionReportsARolloutThatMadeNoModelCalls(t *testing.T) {
	state := classifyExecution(json.RawMessage(`{"result":{"agent_response":"x"},"rollout":{"turns":[]}}`))
	if state.State != executionFailed || state.Error != "No model calls" {
		t.Fatalf("state = %+v, want a failed no-model-calls state", state)
	}
}

// Taken from rollout aea89e72e3fd441b9efe10f1aacaf7e8 of ftjob-f0c92de8, which
// is the case this column exists for: it crashed 7 turns in, and the index
// recorded it with reward 0.057, has_graph=true and an unremarkable 138s
// latency -- indistinguishable there from a rollout that simply scored badly.
// It also carries an exception with no message, so the type must be reported
// without a trailing separator.
func TestClassifyExecutionHandlesAnExceptionThatCarriesNoMessage(t *testing.T) {
	traceback := "ROLLOUT ERROR\nTraceback (most recent call last):\n" +
		"  File \"/app/hosted_agent/pipeline.py\", line 330, in _create_with_retry\n" +
		"    return await self._client.responses.create(**kwargs)\n" +
		"httpx.ReadTimeout"
	state := classifyExecution(json.RawMessage(
		`{"result":{"agent_response":` + mustQuote(traceback) + `},"rollout":{"turns":[{},{},{},{},{},{},{}]}}`))
	if state.State != executionFailed {
		t.Fatalf("state = %q, want %q", state.State, executionFailed)
	}
	if state.Error != "httpx.ReadTimeout" {
		t.Fatalf("error = %q, want httpx.ReadTimeout", state.Error)
	}
	if state.Detail != "" {
		t.Fatalf("detail = %q, want empty", state.Detail)
	}
}

// A body that cannot be read is not evidence the rollout failed, and reporting
// it as one would invent faults a run did not have.
func TestClassifyExecutionTreatsAnUnreadableBodyAsCompleted(t *testing.T) {
	for name, body := range map[string]string{
		"malformed": `{"result":`,
		"empty":     ``,
		"no turns":  `{"result":{"agent_response":"answered"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if state := classifyExecution(json.RawMessage(body)); state.State != executionCompleted {
				t.Fatalf("state = %q, want %q", state.State, executionCompleted)
			}
		})
	}
}

type countingReader struct {
	mu     sync.Mutex
	calls  map[string]int
	failOn map[string]bool
}

func (r *countingReader) Get(_ context.Context, id string) (rollouts.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[id]++
	if r.failOn[id] {
		return rollouts.Snapshot{}, fmt.Errorf("unreachable")
	}
	body := `{"result":{"agent_response":"ok"},"rollout":{"turns":[{}]}}`
	if id == "crashed" {
		body = `{"result":{"agent_response":"ROLLOUT ERROR\nValueError: bad"},"rollout":{"turns":[{}]}}`
	}
	return rollouts.Snapshot{Response: json.RawMessage(body)}, nil
}

func probeFixture(t *testing.T, ids []string, failOn map[string]bool) (*stateProbe, *countingReader) {
	t.Helper()
	entries := make([]rollouts.Entry, 0, len(ids))
	for i, id := range ids {
		entries = append(entries, rollouts.Entry{RolloutID: id, Sequence: i})
	}
	index := newJobIndex("job", nil)
	index.entries = entries
	reader := &countingReader{calls: map[string]int{}, failOn: failOn}
	return newStateProbe(index, reader, 2), reader
}

func TestStateProbeFillsTheRunAndReadsEachRolloutOnce(t *testing.T) {
	probe, reader := probeFixture(t, []string{"a", "crashed", "c"}, nil)

	if added := probe.fill(context.Background(), 12); added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	// A second pass must be free. A body is ~342KB, so re-reading a run the
	// monitor has already classified is the cost this cache exists to avoid.
	if added := probe.fill(context.Background(), 12); added != 0 {
		t.Fatalf("second pass added = %d, want 0", added)
	}
	for _, id := range []string{"a", "crashed", "c"} {
		if reader.calls[id] != 1 {
			t.Fatalf("reader called %d times for %q, want 1", reader.calls[id], id)
		}
	}
	states, known := probe.snapshot()
	if known != 3 {
		t.Fatalf("known = %d, want 3", known)
	}
	if states["crashed"].State != executionFailed || states["crashed"].Error != "ValueError" {
		t.Fatalf("crashed state = %+v, want a failed ValueError state", states["crashed"])
	}
	if states["a"].State != executionCompleted {
		t.Fatalf("a state = %q, want %q", states["a"].State, executionCompleted)
	}
}

func TestStateProbeHonoursTheBatchLimit(t *testing.T) {
	probe, _ := probeFixture(t, []string{"a", "b", "c", "d", "e"}, nil)
	if added := probe.fill(context.Background(), 2); added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if _, known := probe.snapshot(); known != 2 {
		t.Fatalf("known = %d, want 2", known)
	}
}

// A read that failed is a fact about the service, not about the rollout, so it
// must be retried rather than cached as an answer.
func TestStateProbeRetriesARolloutItCouldNotRead(t *testing.T) {
	probe, reader := probeFixture(t, []string{"a", "b"}, map[string]bool{"b": true})

	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if _, known := probe.snapshot(); known != 1 {
		t.Fatalf("known = %d, want 1", known)
	}
	reader.mu.Lock()
	reader.failOn = map[string]bool{}
	reader.mu.Unlock()

	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("retry added = %d, want 1", added)
	}
	if reader.calls["b"] != 2 {
		t.Fatalf("reader called %d times for b, want 2", reader.calls["b"])
	}
}

// The probe runs while the job does, so rollouts appear in the index after it
// has already classified what was there.
func TestStateProbePicksUpRolloutsRecordedAfterTheFirstPass(t *testing.T) {
	probe, _ := probeFixture(t, []string{"a"}, nil)
	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	probe.index.entries = append(probe.index.entries, rollouts.Entry{RolloutID: "b", Sequence: 1})
	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if _, known := probe.snapshot(); known != 2 {
		t.Fatalf("known = %d, want 2", known)
	}
}

func TestStateProbeStopsWhenTheContextIsCancelled(t *testing.T) {
	probe, _ := probeFixture(t, []string{"a", "b", "c"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probe.fill(ctx, 12)
	// Whatever it managed, nothing may be left marked in flight, or those
	// rollouts would never be probed again.
	probe.mu.Lock()
	inflight := len(probe.inflight)
	probe.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("inflight = %d after cancellation, want 0", inflight)
	}
}

func mustQuote(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
