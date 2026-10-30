// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"sync"
	"time"

	"azure.ai.rle/internal/rollouts"
)

// refreshInterval is the shortest gap between two trips to the service.
//
// The page polls, and every open tab polls separately, so without a floor a
// dashboard left open would list the run continuously. A rollout takes minutes,
// so a few seconds of staleness is not visible.
const refreshInterval = 3 * time.Second

// jobIndex is the rollout list of one training job, kept current while the run
// continues.
//
// A run appends rollouts as they finish, and the service returns the index in
// the order it recorded them, so refreshing means asking for what follows the
// last rollout already held rather than reading the whole run again. A poll
// then costs what is new, not how long the run has been going.
type jobIndex struct {
	jobID  string
	lister rollouts.Lister

	// fetchMu serializes trips to the service. A caller that arrives during a
	// refresh waits for it rather than skipping it, because a rollout being
	// opened must not be judged unknown against a list that is mid-flight.
	fetchMu sync.Mutex

	mu        sync.Mutex
	entries   []rollouts.Entry
	lastID    string
	attempted time.Time
}

func newJobIndex(jobID string, lister rollouts.Lister) *jobIndex {
	return &jobIndex{jobID: jobID, lister: lister, entries: []rollouts.Entry{}}
}

// fetch asks the service for the rollouts recorded since the last call and
// reports how many were added.
func (j *jobIndex) fetch(ctx context.Context) (int, error) {
	j.fetchMu.Lock()
	defer j.fetchMu.Unlock()
	return j.fetchLocked(ctx)
}

// fetchLocked performs the trip. The caller must hold fetchMu.
func (j *jobIndex) fetchLocked(ctx context.Context) (int, error) {
	j.mu.Lock()
	after := j.lastID
	j.mu.Unlock()

	added, listErr := j.lister.List(ctx, after)

	j.mu.Lock()
	defer j.mu.Unlock()
	// The interval limits attempts, not successes: a service that is failing
	// should not be retried on every request the page makes.
	j.attempted = time.Now()
	if listErr != nil {
		return 0, listErr
	}
	if len(added) == 0 {
		return 0, nil
	}
	j.entries = append(j.entries, added...)
	j.lastID = added[len(added)-1].RolloutID
	return len(added), nil
}

// entriesAfter returns the rollouts recorded after the given id, refreshing
// first when the list has gone stale.
//
// The second result reports that the answer is the whole list rather than an
// increment, which happens when the caller's id is not recognized. A caller
// that is appending has to replace instead, or it would double every rollout.
//
// A failed refresh is not reported. The rollouts already listed are still worth
// showing, and one failed call should not blank the page; the next poll retries.
func (j *jobIndex) entriesAfter(ctx context.Context, after string) ([]rollouts.Entry, bool) {
	if j.stale() {
		j.fetchMu.Lock()
		// Another caller may have refreshed while this one waited.
		if j.stale() {
			_, _ = j.fetchLocked(ctx)
		}
		j.fetchMu.Unlock()
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if after != "" {
		for position, entry := range j.entries {
			if entry.RolloutID == after {
				return append([]rollouts.Entry(nil), j.entries[position+1:]...), false
			}
		}
		// The caller holds an id this index does not recognize. Returning the
		// whole list resynchronizes the page rather than leaving it stuck.
		return append([]rollouts.Entry(nil), j.entries...), true
	}
	return append([]rollouts.Entry(nil), j.entries...), false
}

// contains reports whether the job recorded this rollout.
//
// A miss is retried against the service, because the rollout may have been
// recorded since the last refresh, and refusing it would make a rollout the
// page is already showing impossible to open.
func (j *jobIndex) contains(ctx context.Context, rolloutID string) bool {
	if j.has(rolloutID) {
		return true
	}
	j.fetchMu.Lock()
	defer j.fetchMu.Unlock()
	// A refresh may have landed it while this caller waited for the lock.
	if j.has(rolloutID) {
		return true
	}
	if _, err := j.fetchLocked(ctx); err != nil {
		return false
	}
	return j.has(rolloutID)
}

func (j *jobIndex) has(rolloutID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, entry := range j.entries {
		if entry.RolloutID == rolloutID {
			return true
		}
	}
	return false
}

func (j *jobIndex) stale() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return time.Since(j.attempted) >= refreshInterval
}

func (j *jobIndex) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

// rolloutIDs returns the rollouts listed so far, oldest first. The order is the
// run's own, so a probe reading it front to back classifies the run in the
// order it happened rather than in whatever order a map produced.
func (j *jobIndex) rolloutIDs() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	ids := make([]string, 0, len(j.entries))
	for _, entry := range j.entries {
		ids = append(ids, entry.RolloutID)
	}
	return ids
}
