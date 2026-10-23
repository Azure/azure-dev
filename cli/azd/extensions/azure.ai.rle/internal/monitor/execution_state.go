// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"strings"
	"sync"
	"time"

	"azure.ai.rle/internal/rollouts"
)

// The harness grades a rollout whether or not it finished, so the index's
// `success` is a task verdict and says nothing about execution. A rollout
// killed by a transport error still carries a reward, a graph and
// success=false, which is indistinguishable at index level from a complete but
// poor attempt: the crashed rollout aea89e72 recorded reward 0.057,
// has_graph=true and an unremarkable 138s latency. Only the agent's own output
// separates them, and that lives in the body.
//
// This matters beyond presentation. A rollout that died mid-flight is still
// graded and still trained on, and because a crash returns `parsed: false` it
// zeroes four reward dimensions -- so a good trajectory enters the gradient
// labelled ~0.13. The share of a run that failed to execute is therefore a
// reward-contamination measure, not a cosmetic one.
const rolloutErrorPrefix = "ROLLOUT ERROR"

// pythonException matches the `Type: message` line a traceback ends with.
// Kept in step with PYTHON_EXCEPTION in web/data.mjs, which classifies the one
// rollout the detail page has open; this classifies them in bulk.
var pythonException = regexp.MustCompile(`^([A-Za-z_][\w.]*(?:Error|Exception|Timeout|Interrupt))(?::\s*(.*))?$`)

// executionState is whether the rollout ran to completion, as opposed to how
// well it scored.
type executionState struct {
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const (
	executionCompleted = "completed"
	executionFailed    = "failed"
)

// exceptionFromTraceback returns the exception that escaped. A traceback ends
// with the one that stopped the rollout, and the earlier matches are the frames
// it wrapped, so the last match is the answer rather than the first.
func exceptionFromTraceback(text string) (string, string) {
	kind, message := "", ""
	for line := range strings.SplitSeq(text, "\n") {
		if match := pythonException.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			kind, message = match[1], strings.TrimSpace(match[2])
		}
	}
	return kind, message
}

// classifyExecution reads only the two fields that carry execution outcome, so
// a 342KB body does not have to be held or re-encoded to answer a question
// worth a dozen bytes.
func classifyExecution(response json.RawMessage) executionState {
	if len(response) == 0 {
		return executionState{State: executionCompleted}
	}
	var body struct {
		Result struct {
			AgentResponse string `json:"agent_response"`
		} `json:"result"`
		RolloutGraph struct {
			Turns []json.RawMessage `json:"turns"`
		} `json:"rollout_graph"`
		// RolloutGraphLegacy supports RLE services that have not yet picked up
		// the rollout -> rollout_graph rename.
		RolloutGraphLegacy struct {
			Turns []json.RawMessage `json:"turns"`
		} `json:"rollout"`
	}
	// An unreadable body is not evidence the rollout failed, so it is left as
	// completed rather than reported as a failure the run did not have.
	if err := json.Unmarshal(response, &body); err != nil {
		return executionState{State: executionCompleted}
	}
	if strings.HasPrefix(body.Result.AgentResponse, rolloutErrorPrefix) {
		kind, message := exceptionFromTraceback(body.Result.AgentResponse)
		return executionState{State: executionFailed, Error: kind, Detail: message}
	}
	turns := body.RolloutGraph.Turns
	if turns == nil {
		turns = body.RolloutGraphLegacy.Turns
	}
	if turns != nil && len(turns) == 0 {
		return executionState{
			State:  executionFailed,
			Error:  "No model calls",
			Detail: "The rollout recorded no turns.",
		}
	}
	return executionState{State: executionCompleted}
}

// stateProbe fills in execution state for the rollouts an index lists.
//
// The work is done here rather than in the page because a body is ~342KB and a
// run records thousands: asking the browser to classify them would move
// hundreds of megabytes to answer a per-row yes/no, which is the reason the
// monitor fetches bodies only as they are opened. Probing server-side keeps
// that property -- the page receives a few bytes per rollout -- and lets the
// results be shared between reloads.
//
// Concurrency is deliberately small. The service being read is the one running
// the training job, so the probe is meant to be a trickle alongside it rather
// than a second workload.
type stateProbe struct {
	reader  rollouts.Reader
	index   *jobIndex
	workers int

	mu       sync.Mutex
	states   map[string]executionState
	inflight map[string]struct{}
	// Rollouts someone is looking at right now, classified ahead of the sweep.
	// Held as a queue rather than a set so the order a viewer asked in is the
	// order they are answered in.
	wanted []string
}

func newStateProbe(index *jobIndex, reader rollouts.Reader, workers int) *stateProbe {
	if workers < 1 {
		workers = 1
	}
	return &stateProbe{
		reader:   reader,
		index:    index,
		workers:  workers,
		states:   map[string]executionState{},
		inflight: map[string]struct{}{},
	}
}

// snapshot returns what is known so far, and how much of the run it covers.
func (p *stateProbe) snapshot() (map[string]executionState, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]executionState, len(p.states))
	maps.Copy(out, p.states)
	return out, len(p.states)
}

// prioritize moves rollouts to the front of the probe's work.
//
// The sweep runs oldest first, which is the order the run happened in but not
// the order anyone reads it: a viewer filters the list to the step that just
// landed, and those are the last rollouts the sweep would reach. On a long run
// that is the difference between a column that fills while you look at it and
// one that arrives an hour later, so the page sends what it is showing and
// those are classified next.
//
// Already-known and in-flight rollouts are dropped here rather than queued, so
// a page repeating its request every poll does not grow the queue.
//
// Only rollouts this job recorded are queued, so the request cannot be used to
// pull an arbitrary ID through the operator's credentials. An ID missing from
// the index is skipped rather than looked up: this is a hint about what is on
// screen, and a page showing a rollout necessarily got it from the index, so a
// miss means a stale index and the next poll asks again.
func (p *stateProbe) prioritize(ids []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	queued := make(map[string]struct{}, len(p.wanted))
	for _, id := range p.wanted {
		queued[id] = struct{}{}
	}
	for _, id := range ids {
		if len(p.wanted) >= stateProbeWanted {
			return
		}
		if id == "" || !p.index.has(id) {
			continue
		}
		if _, done := p.states[id]; done {
			continue
		}
		if _, busy := p.inflight[id]; busy {
			continue
		}
		if _, already := queued[id]; already {
			continue
		}
		queued[id] = struct{}{}
		p.wanted = append(p.wanted, id)
	}
}

// pending returns rollouts the probe has neither classified nor started, up to
// limit, and marks them in flight. Rollouts a viewer asked for come first; the
// rest of the batch continues the sweep, so asking for one screen of rollouts
// does not stop the run from being classified in the background.
func (p *stateProbe) pending(ids []string, limit int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	queued := make([]string, 0, limit)
	taken := make(map[string]struct{}, limit)
	claim := func(id string) bool {
		if _, done := p.states[id]; done {
			return false
		}
		if _, busy := p.inflight[id]; busy {
			return false
		}
		if _, dup := taken[id]; dup {
			return false
		}
		p.inflight[id] = struct{}{}
		taken[id] = struct{}{}
		queued = append(queued, id)
		return true
	}
	// A request is consumed whether or not it was claimable: an id that is
	// already known or in flight is answered, and leaving it queued would make
	// the same rollout block the front of the queue on every pass.
	for len(p.wanted) > 0 && len(queued) < limit {
		id := p.wanted[0]
		p.wanted = p.wanted[1:]
		claim(id)
	}
	for _, id := range ids {
		if len(queued) == limit {
			break
		}
		claim(id)
	}
	return queued
}

func (p *stateProbe) record(id string, state executionState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.states[id] = state
	delete(p.inflight, id)
}

func (p *stateProbe) release(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, id)
}

// fill classifies up to limit unknown rollouts and returns how many it added.
// A rollout that cannot be read is released rather than recorded, so a
// transient failure is retried on a later pass instead of being cached as an
// answer.
func (p *stateProbe) fill(ctx context.Context, limit int) int {
	queued := p.pending(p.index.rolloutIDs(), limit)
	if len(queued) == 0 {
		return 0
	}
	work := make(chan string)
	var added int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range min(p.workers, len(queued)) {
		wg.Go(func() {
			for id := range work {
				snapshot, err := p.reader.Get(ctx, id)
				if err != nil {
					p.release(id)
					continue
				}
				p.record(id, classifyExecution(snapshot.Response))
				mu.Lock()
				added++
				mu.Unlock()
			}
		})
	}
	for _, id := range queued {
		select {
		case <-ctx.Done():
			p.release(id)
		case work <- id:
		}
	}
	close(work)
	wg.Wait()
	return added
}

// Tuned to be a trickle beside the training job rather than a second workload:
// the service being read is the one running the run. A batch of 12 at three
// workers is roughly three seconds of work, after which the loop yields.
const (
	stateProbeWorkers = 3
	stateProbeBatch   = 12
	stateProbeIdle    = 10 * time.Second
	stateProbeBusy    = 500 * time.Millisecond
	// One screen of rollouts is well under this; the cap is only here so a
	// caller asking for a whole run at once cannot make the queue the memory
	// the probe exists to avoid spending.
	stateProbeWanted = 512
)

// run classifies the run in the background for as long as the monitor is up.
//
// Filling here rather than inside the request keeps the endpoint a pure read,
// so a page poll costs a map lookup instead of waiting on however many bodies
// happened to be unclassified. The loop backs off when it finds nothing, which
// is the steady state once a run is fully classified and new rollouts arrive
// every few minutes.
func (p *stateProbe) run(ctx context.Context) {
	for {
		delay := stateProbeIdle
		if p.fill(ctx, stateProbeBatch) > 0 {
			delay = stateProbeBusy
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}
