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
		`{"result":{"agent_response":"The competitor raised a Series B."},"rollout_graph":{"turns":[{},{}]}}`))
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
		`{"result":{"agent_response":` + mustQuote(traceback) + `},"rollout_graph":{"turns":[{}]}}`))
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
	state := classifyExecution(json.RawMessage(`{"result":{"agent_response":"x"},"rollout_graph":{"turns":[]}}`))
	if state.State != executionFailed || state.Error != "No model calls" {
		t.Fatalf("state = %+v, want a failed no-model-calls state", state)
	}
}

// Some RLE services have not yet picked up the rollout -> rollout_graph
// rename; classification must agree either way.
func TestClassifyExecutionAcceptsTheLegacyRolloutField(t *testing.T) {
	state := classifyExecution(json.RawMessage(`{"result":{"agent_response":"x"},"rollout":{"turns":[]}}`))
	if state.State != executionFailed || state.Error != "No model calls" {
		t.Fatalf("state = %+v, want a failed no-model-calls state", state)
	}
	completed := classifyExecution(json.RawMessage(
		`{"result":{"agent_response":"x"},"rollout":{"turns":[{},{}]}}`))
	if completed.State != executionCompleted {
		t.Fatalf("state = %q, want %q", completed.State, executionCompleted)
	}
	// rollout_graph wins when a response somehow carries both.
	both := classifyExecution(json.RawMessage(
		`{"result":{"agent_response":"x"},"rollout_graph":{"turns":[{}]},"rollout":{"turns":[]}}`))
	if both.State != executionCompleted {
		t.Fatalf("state = %q, want %q (rollout_graph should win)", both.State, executionCompleted)
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
		`{"result":{"agent_response":` + mustQuote(traceback) + `},"rollout_graph":{"turns":[{},{},{},{},{},{},{}]}}`))
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
	body := `{"result":{"agent_response":"ok"},"rollout_graph":{"turns":[{}]}}`
	if id == "crashed" {
		body = `{"result":{"agent_response":"ROLLOUT ERROR\nValueError: bad"},"rollout_graph":{"turns":[{}]}}`
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

// The sweep runs oldest first, but a viewer reads a training run newest first:
// they filter the list to the step that just landed. Those rollouts are the
// last the sweep would reach, so on a long run the column stays empty exactly
// where someone is looking. These pin the request that fixes that.

func TestStateProbeClassifiesRequestedRolloutsBeforeTheSweepReachesThem(t *testing.T) {
	probe, _ := probeFixture(t, []string{"a", "b", "c", "d", "e", "f"}, nil)
	probe.prioritize([]string{"f", "e"})

	// A batch of three: the two asked for, then the sweep carries on from the
	// front, so asking for a screenful does not stall the background pass.
	if added := probe.fill(context.Background(), 3); added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	states, _ := probe.snapshot()
	for _, id := range []string{"f", "e"} {
		if _, ok := states[id]; !ok {
			t.Fatalf("requested rollout %q was not classified first, got %v", id, states)
		}
	}
	if _, ok := states["a"]; !ok {
		t.Fatalf("the sweep did not advance alongside the request, got %v", states)
	}
	if _, ok := states["b"]; ok {
		t.Fatalf("the batch limit was exceeded, got %v", states)
	}
}

// The page repeats its request on every poll, so a request has to be a hint
// rather than an instruction to read: re-asking for a rollout already answered
// must not read the ~342KB body again.
func TestStateProbeRereadsNothingWhenAViewerKeepsAsking(t *testing.T) {
	probe, reader := probeFixture(t, []string{"a", "b", "c"}, nil)
	for range 3 {
		probe.prioritize([]string{"c"})
	}
	if added := probe.fill(context.Background(), 12); added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	probe.prioritize([]string{"c", "a"})
	if added := probe.fill(context.Background(), 12); added != 0 {
		t.Fatalf("a request for classified rollouts added = %d, want 0", added)
	}
	if reader.calls["c"] != 1 {
		t.Fatalf("reader called %d times for c, want 1", reader.calls["c"])
	}
	if len(probe.wanted) != 0 {
		t.Fatalf("queue retained %d ids across polls, want 0", len(probe.wanted))
	}
}

// A request that could not be served is dropped rather than left at the front
// of the queue, where it would block every later request behind it.
func TestStateProbeDoesNotLetAnUnreadableRequestBlockTheQueue(t *testing.T) {
	probe, reader := probeFixture(t, []string{"a", "b"}, map[string]bool{"a": true})
	probe.prioritize([]string{"a", "b"})

	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if len(probe.wanted) != 0 {
		t.Fatalf("queue = %v, want it drained", probe.wanted)
	}
	// Still retried, by the sweep or by the page asking again.
	reader.mu.Lock()
	reader.failOn = map[string]bool{}
	reader.mu.Unlock()
	if added := probe.fill(context.Background(), 12); added != 1 {
		t.Fatalf("retry added = %d, want 1", added)
	}
}

// The queue is a hint from whatever is on screen, so its size is bounded by
// the caller. It must not become the memory the probe exists to avoid.
func TestStateProbeBoundsWhatOneRequestCanQueue(t *testing.T) {
	ids := make([]string, 0, stateProbeWanted*2)
	for i := range stateProbeWanted * 2 {
		ids = append(ids, fmt.Sprintf("r%d", i))
	}
	probe, _ := probeFixture(t, ids, nil)
	probe.prioritize(ids)
	probe.prioritize(ids)

	if len(probe.wanted) > stateProbeWanted {
		t.Fatalf("queue = %d, want at most %d", len(probe.wanted), stateProbeWanted)
	}
}

func TestStateProbeIgnoresRolloutIdsThatAreNotInTheRun(t *testing.T) {
	probe, reader := probeFixture(t, []string{"a"}, nil)
	// Nothing stops a caller naming anything; the reader is only ever asked
	// for it if the probe queues it, so an unknown id must not reach the
	// service under the operator's credentials.
	probe.prioritize([]string{"", "not-in-this-run"})
	probe.fill(context.Background(), 12)

	if _, asked := reader.calls["not-in-this-run"]; asked {
		t.Fatalf("probe fetched a rollout the run never recorded")
	}
	if _, asked := reader.calls[""]; asked {
		t.Fatalf("probe fetched an empty rollout id")
	}
}
