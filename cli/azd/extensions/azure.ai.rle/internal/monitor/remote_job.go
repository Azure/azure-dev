// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"azure.ai.rle/internal/rollouts"
)

const (
	remoteRolloutLimit = 10000
	remoteMetricBytes  = 32 << 20
	pagesPerRefresh    = 10

	// Every discovery is a full, oldest-first scan of the job: the service has
	// no ID or timestamp anchor, and a scan advances only by continuation
	// token. Running rows are also refreshed by point reads: those the page is
	// showing every 10 seconds, the rest about once a minute.
	rolloutDiscoverEvery = time.Minute
	rolloutWatchedEvery  = 10 * time.Second
	rolloutRunningEvery  = time.Minute
	runningPerRefresh    = 50
	// The page names the rows it shows on every poll, so a list it stopped
	// naming for this long is no longer on screen.
	watchExpiry = 30 * time.Second
	// A forced refresh answers when its cycle finishes, or after this long.
	refreshWaitLimit = 45 * time.Second
)

// Reasons automatic polling stopped, reported to the page as "paused".
const (
	pausedCompleted = "completed"
	pausedDisabled  = "disabled"
)

type pollState struct {
	Next     time.Time `json:"-"`
	RetryAt  time.Time `json:"-"`
	Failures int       `json:"-"`
	Error    string    `json:"error,omitempty"`
	Updated  time.Time `json:"updated_at,omitzero"`
}

// pageCursor walks RLE watermark pages. Incremental cycles start at the committed
// watermark; every `every`, a full cycle starts at floor instead, picking up late
// lower IDs and status changes within the retained window. The service keeps the
// watermark query fixed across continuation pages, so it only advances once a
// cycle reaches its last page.
type pageCursor struct {
	watermark, floor, start, maximum int64

	token     string
	seen      map[string]bool
	full      bool
	completed time.Time
	every     time.Duration
}

func newPageCursor(every time.Duration) pageCursor {
	return pageCursor{watermark: -1, floor: -1, every: every}
}

func (p *pageCursor) begin(now time.Time) {
	if p.seen != nil {
		return // Resume the cycle a previous refresh left unfinished.
	}
	p.full = p.completed.IsZero() || now.Sub(p.completed) >= p.every
	p.start = p.watermark
	if p.full {
		p.start = p.floor
	}
	p.maximum = p.watermark
	p.seen = map[string]bool{}
}

func (p *pageCursor) reset() {
	p.token, p.seen = "", nil
}

func (p *pageCursor) advance(token string, now time.Time) (bool, error) {
	if token == "" {
		p.watermark = p.maximum
		p.reset()
		if p.full {
			p.completed = now
		}
		return true, nil
	}
	if p.seen[token] {
		p.reset()
		return false, errors.New("service repeated a continuation token")
	}
	p.seen[token] = true
	p.token = token
	return false, nil
}

type remoteJob struct {
	source      JobSource
	jobID       string
	force       chan chan struct{}
	resultSlots chan struct{}

	fetchMu sync.Mutex // Serializes refresh cycles.

	mu         sync.Mutex // Guards the fields below.
	config     json.RawMessage
	metrics    map[int64]json.RawMessage
	entries    map[string]JobRollout
	readAt     map[string]time.Time // When each cached rollout was last read.
	states     map[string]pollState
	status     string
	terminalAt time.Time
	paused     string
	limited    bool
	// watched names the running rollouts the page last reported showing.
	watched   []string
	watchedAt time.Time

	metricCursor pageCursor
	// rolloutScan is the scan in progress, if a refresh left one unfinished.
	// rolloutsReconciled is when the last completed scan started.
	rolloutScan        *rolloutScan
	rolloutsReconciled time.Time
	// rolloutTotal is how many rollouts the last completed scan listed. The
	// cache keeps only the newest remoteRolloutLimit, so this is the true count.
	rolloutTotal int
}

// rolloutScan follows the job's rollout list from its first page, without a
// token, to the page that has no continuation token.
type rolloutScan struct {
	started time.Time
	token   string
	seen    map[string]bool
	listed  int // Rollouts read so far; pages of one scan do not overlap.
}

func newRemoteJob(source JobSource, jobID string) *remoteJob {
	return &remoteJob{
		source: source, jobID: jobID,
		force: make(chan chan struct{}, 1), resultSlots: make(chan struct{}, 4),
		metrics: map[int64]json.RawMessage{}, entries: map[string]JobRollout{}, readAt: map[string]time.Time{},
		states:       map[string]pollState{},
		metricCursor: newPageCursor(time.Minute),
	}
}

// RunRemoteJob serves API-backed training data without creating a local mirror.
func RunRemoteJob(ctx context.Context, reader JobSource, jobID string, noBrowser bool, out, errOut io.Writer) error {
	return serve(ctx, source{jobID: jobID, remote: newRemoteJob(reader, jobID)}, noBrowser, out, errOut)
}

func (j *remoteJob) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	j.refresh(ctx, time.Now(), false)
	for {
		select {
		case <-ctx.Done():
			return
		case done := <-j.force:
			j.refresh(ctx, time.Now(), true)
			close(done)
		case <-ticker.C:
			j.refresh(ctx, time.Now(), false)
		}
	}
}

func (j *remoteJob) poll(ctx context.Context, now time.Time, name string, interval time.Duration, read func() error) {
	if ctx.Err() != nil {
		return
	}
	j.mu.Lock()
	state := j.states[name]
	j.mu.Unlock()
	if now.Before(state.Next) {
		return
	}
	err := read()
	if ctx.Err() != nil {
		return
	}
	// Charge request duration to the schedule, so a slow service never causes
	// an immediate catch-up loop. Synthetic test clocks may be ahead of wall time.
	finished := time.Now()
	if finished.Before(now) {
		finished = now
	}
	state.Next, state.RetryAt = finished.Add(interval), time.Time{}
	disabled := false
	if err == nil {
		state.Failures, state.Error, state.Updated = 0, "", now
	} else {
		state.Error = err.Error()
		state.Failures = min(state.Failures+1, 6)
		delay := min(interval<<state.Failures, 2*time.Minute)
		if serviceErr, ok := errors.AsType[*ReadError](err); ok {
			switch serviceErr.Code {
			case "JobNotFound":
				delay = interval
				state.Error = "Waiting for job registration (or the job ID is unknown in this project)."
			case "FeatureDisabled":
				disabled = true
				state.Error = "The RLE job API is not enabled for this project."
			}
			if serviceErr.RetryAfter > 0 {
				state.RetryAt = finished.Add(serviceErr.RetryAfter)
				delay = max(delay, serviceErr.RetryAfter)
			}
		}
		state.Next = finished.Add(delay)
	}
	j.mu.Lock()
	j.states[name] = state
	if disabled {
		j.paused = pausedDisabled
	}
	j.mu.Unlock()
}

func (j *remoteJob) refresh(ctx context.Context, now time.Time, force bool) {
	j.fetchMu.Lock()
	defer j.fetchMu.Unlock()
	j.mu.Lock()
	if force {
		j.paused = ""
		for key, state := range j.states {
			// Retry failed reads now, except where the service asked to wait.
			state.Next = state.RetryAt
			j.states[key] = state
		}
		j.metricCursor.completed, j.rolloutsReconciled = time.Time{}, time.Time{}
		if !j.terminalAt.IsZero() {
			j.terminalAt = now
		}
	}
	paused, haveConfig, terminal := j.paused != "", len(j.config) != 0, !j.terminalAt.IsZero()
	j.mu.Unlock()
	if paused {
		return
	}
	var reads sync.WaitGroup
	if !haveConfig {
		reads.Go(func() {
			j.poll(ctx, now, "config", 5*time.Second, func() error {
				raw, err := j.source.Config(ctx)
				if err == nil {
					j.mu.Lock()
					j.config = raw
					j.mu.Unlock()
				}
				return err
			})
		})
	}
	reads.Go(func() {
		j.poll(ctx, now, "metrics", 10*time.Second, func() error { return j.readMetrics(ctx, now) })
	})
	reads.Go(func() {
		j.poll(ctx, now, "rollouts", rolloutDiscoverEvery, func() error { return j.readRollouts(ctx, now) })
		j.mu.Lock()
		if j.rolloutScan != nil {
			// Continue an unfinished scan on the next tick instead of a minute later.
			state := j.states["rollouts"]
			state.Next = now
			j.states["rollouts"] = state
		}
		j.mu.Unlock()
		// After the list, so rows it just returned are not point-read again.
		j.poll(ctx, now, "running", rolloutWatchedEvery, func() error { return j.refreshRunning(ctx, now) })
	})
	if !terminal {
		reads.Go(func() {
			j.poll(ctx, now, "status", time.Minute, func() error {
				status, err := j.source.Status(ctx)
				if err != nil {
					return err
				}
				j.mu.Lock()
				defer j.mu.Unlock()
				j.status = status
				if status == "succeeded" || status == "failed" || status == "cancelled" {
					j.terminalAt = now
				}
				return nil
			})
		})
	}
	reads.Wait()

	// Stop once the job has finished, settled for a minute, and a full
	// reconciliation started after settling has completed without errors.
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.paused != "" || j.terminalAt.IsZero() || now.Sub(j.terminalAt) < time.Minute || len(j.config) == 0 {
		return
	}
	settled := j.terminalAt.Add(time.Minute)
	for _, state := range j.states {
		if state.Error != "" {
			return
		}
	}
	if !j.metricCursor.completed.Before(settled) && !j.rolloutsReconciled.Before(settled) {
		j.paused = pausedCompleted
	}
}

func (j *remoteJob) readMetrics(ctx context.Context, now time.Time) error {
	p := &j.metricCursor
	p.begin(now)
	for range pagesPerRefresh {
		page, err := j.source.Metrics(ctx, p.start, p.token)
		if err != nil {
			p.reset() // Restart at the committed watermark.
			return err
		}
		rows := make(map[int64]json.RawMessage, len(page.Data))
		for _, raw := range page.Data {
			var row struct {
				Step *int64 `json:"step"`
			}
			if err := json.Unmarshal(raw, &row); err != nil || row.Step == nil || *row.Step < 0 {
				p.reset()
				return errors.New("metric row is missing a nonnegative integer step")
			}
			rows[*row.Step] = raw
			p.maximum = max(p.maximum, *row.Step)
		}
		j.mu.Lock()
		maps.Copy(j.metrics, rows)
		j.trimMetrics()
		j.mu.Unlock()
		if done, err := p.advance(page.Next, now); done || err != nil {
			return err
		}
	}
	return nil
}

// trimMetrics evicts the oldest steps beyond the row and byte bounds, and moves
// the reconciliation floor so evicted rows are not reread.
func (j *remoteJob) trimMetrics() {
	keys := slices.Sorted(maps.Keys(j.metrics))
	size := 0
	for _, raw := range j.metrics {
		size += len(raw)
	}
	evicted := false
	for len(keys) > 1 && (len(keys) > maxRunMetricLines || size > remoteMetricBytes) {
		size -= len(j.metrics[keys[0]])
		delete(j.metrics, keys[0])
		keys = keys[1:]
		evicted = true
	}
	if evicted {
		j.limited = true
		j.metricCursor.floor = keys[0] - 1
	}
}

// readRollouts continues the current scan, or starts a new one without a
// token, and upserts each page by rollout ID. Pages may be empty and still
// carry a token. A refresh reads a bounded number of pages; the next one
// resumes the scan.
func (j *remoteJob) readRollouts(ctx context.Context, now time.Time) error {
	restarted := false
	for range pagesPerRefresh {
		if j.rolloutScan == nil {
			j.mu.Lock()
			j.rolloutScan = &rolloutScan{started: now, seen: map[string]bool{}}
			j.mu.Unlock()
		}
		scan := j.rolloutScan
		page, err := j.source.Rollouts(ctx, scan.token)
		if serviceErr, ok := errors.AsType[*ReadError](err); ok && serviceErr.Status == http.StatusBadRequest &&
			!restarted && scan.token != "" {
			// The service rejected a stale paging token; scan the job again.
			j.mu.Lock()
			j.rolloutScan, restarted = nil, true
			j.mu.Unlock()
			continue
		}
		j.mu.Lock()
		if err != nil {
			j.rolloutScan = nil
			j.mu.Unlock()
			return err
		}
		for _, entry := range page.Data {
			if previous, ok := j.entries[entry.RolloutID]; ok && entry.CreatedAt.IsZero() {
				entry.CreatedAt = previous.CreatedAt
			}
			j.entries[entry.RolloutID] = entry
			j.readAt[entry.RolloutID] = now
		}
		scan.listed += len(page.Data)
		j.trimRollouts()
		switch {
		case page.Next == "":
			j.rolloutScan, j.rolloutsReconciled = nil, scan.started
			j.rolloutTotal = scan.listed
			j.mu.Unlock()
			return nil
		case scan.seen[page.Next]:
			j.rolloutScan = nil
			j.mu.Unlock()
			return errors.New("service repeated a continuation token")
		}
		scan.seen[page.Next] = true
		scan.token = page.Next
		j.mu.Unlock()
	}
	return nil
}

func rolloutSettled(status string) bool {
	switch status {
	case "", "completed", "failed", "cancelled":
		return true
	}
	return false
}

// refreshRunning point-reads cached rollouts that are still running and were
// not read recently, so the page sees status and result changes between full
// scans. Rows the page is showing come first and are due sooner; a read is
// skipped when the last one was under half an interval ago.
func (j *remoteJob) refreshRunning(ctx context.Context, now time.Time) error {
	j.mu.Lock()
	watched := map[string]bool{}
	if now.Sub(j.watchedAt) < watchExpiry {
		for _, id := range j.watched {
			watched[id] = true
		}
	}
	var ids, others []string
	for _, entry := range j.sortedEntries() {
		id, age := entry.RolloutID, now.Sub(j.readAt[entry.RolloutID])
		switch {
		case rolloutSettled(entry.Status):
		case watched[id] && age >= rolloutWatchedEvery/2:
			ids = append(ids, id)
		case !watched[id] && age >= rolloutRunningEvery-rolloutWatchedEvery/2:
			others = append(others, id)
		}
	}
	j.mu.Unlock()
	// Then the newest other running rows; older stragglers are caught by the next scan.
	ids = ids[:min(len(ids), runningPerRefresh)]
	ids = append(ids, others[max(0, len(others)-(runningPerRefresh-len(ids))):]...)
	var (
		reads    sync.WaitGroup
		firstErr error
	)
	slots := make(chan struct{}, 4)
	for _, id := range ids {
		reads.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			entry, err := j.source.Detail(ctx, id)
			if err == nil && entry.JobID != j.jobID {
				err = errors.New("rollout metadata does not belong to the monitored job")
			}
			j.mu.Lock()
			defer j.mu.Unlock()
			previous, cached := j.entries[id]
			switch serviceErr, _ := errors.AsType[*ReadError](err); {
			case serviceErr != nil && serviceErr.Status == http.StatusNotFound:
				delete(j.entries, id)
				delete(j.readAt, id)
			case err != nil:
				if firstErr == nil {
					firstErr = err
				}
			case cached: // Never resurrect a row the cache bound evicted meanwhile.
				if entry.CreatedAt.IsZero() {
					entry.CreatedAt = previous.CreatedAt
				}
				j.entries[id] = entry
				j.readAt[id] = now
			}
		})
	}
	reads.Wait()
	return firstErr
}

func (j *remoteJob) trimRollouts() {
	excess := len(j.entries) - remoteRolloutLimit
	if excess <= 0 {
		return
	}
	for _, entry := range j.sortedEntries()[:excess] {
		delete(j.entries, entry.RolloutID)
		delete(j.readAt, entry.RolloutID)
	}
	j.limited = true
}

// totalRollouts reports the job's rollout count, which can exceed the cache.
// Callers hold j.mu.
func (j *remoteJob) totalRollouts() int {
	total := max(j.rolloutTotal, len(j.entries))
	if j.rolloutScan != nil {
		total = max(total, j.rolloutScan.listed)
	}
	return total
}

func (j *remoteJob) sortedEntries() []JobRollout {
	entries := append([]JobRollout{}, slices.Collect(maps.Values(j.entries))...)
	slices.SortFunc(entries, func(a, b JobRollout) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.RolloutID, b.RolloutID))
	})
	return entries
}

func registerRemoteRoutes(mux *http.ServeMux, j *remoteJob) {
	mux.HandleFunc("GET /api/run", func(w http.ResponseWriter, r *http.Request) {
		j.mu.Lock()
		defer j.mu.Unlock()
		writeRunJSON(w, map[string]any{
			"job_id": j.jobID, "backend": "rle", "states": j.states, "paused": j.paused, "limited": j.limited,
			"run": map[string]any{"config": j.config, "meta": map[string]any{"status": j.status}},
		})
	})
	mux.HandleFunc("GET /api/run/metrics", func(w http.ResponseWriter, r *http.Request) {
		j.mu.Lock()
		defer j.mu.Unlock()
		rows := make([]json.RawMessage, 0, len(j.metrics))
		for _, step := range slices.Sorted(maps.Keys(j.metrics)) {
			rows = append(rows, j.metrics[step])
		}
		writeRunJSON(w, map[string]any{"job_id": j.jobID, "data": rows})
	})
	mux.HandleFunc("GET /api/rollouts", func(w http.ResponseWriter, r *http.Request) {
		j.mu.Lock()
		defer j.mu.Unlock()
		if r.URL.Query().Has("watch") {
			j.watched, j.watchedAt = parseWatched(r.URL.Query().Get("watch")), time.Now()
		}
		writeRunJSON(w, map[string]any{
			"job_id": j.jobID, "backend": "rle", "data": j.sortedEntries(), "reset": true,
			"total": j.totalRollouts(),
		})
	})
	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		// Answer once the forced cycle has finished, so the page can show its
		// result instead of a request that may still be queued.
		ctx, cancel := context.WithTimeout(r.Context(), refreshWaitLimit)
		defer cancel()
		done := make(chan struct{})
		select {
		case j.force <- done:
		case <-ctx.Done():
			w.WriteHeader(http.StatusAccepted)
			return
		}
		select {
		case <-done:
			w.WriteHeader(http.StatusNoContent)
		case <-ctx.Done():
			w.WriteHeader(http.StatusAccepted)
		}
	})
	mux.HandleFunc("GET /api/rollout", func(w http.ResponseWriter, r *http.Request) {
		select {
		case j.resultSlots <- struct{}{}:
			defer func() { <-j.resultSlots }()
		case <-r.Context().Done():
			return
		}
		id := r.URL.Query().Get("id")
		if err := rollouts.ValidateID(id); err != nil {
			http.Error(w, "invalid rollout ID", http.StatusBadRequest)
			return
		}
		// The point read checks job ownership even if the row left the bounded cache.
		entry, err := j.source.Detail(r.Context(), id)
		if err != nil {
			writeRemoteReadError(w, fmt.Errorf("read rollout metadata: %w", err))
			return
		}
		if entry.JobID != j.jobID {
			http.Error(w, "rollout does not belong to this job", http.StatusNotFound)
			return
		}
		if !entry.ResultAvailable {
			writeRunJSON(w, map[string]any{"unavailable": true, "metadata": entry})
			return
		}
		snapshot, err := j.source.Result(r.Context(), entry)
		if serviceErr, ok := errors.AsType[*ReadError](err); ok && serviceErr.Status == http.StatusNotFound {
			writeRunJSON(w, map[string]any{"unavailable": true, "metadata": entry})
			return
		}
		if err != nil {
			writeRemoteReadError(w, fmt.Errorf("read rollout result: %w", err))
			return
		}
		writeRunJSON(w, snapshot)
	})
	mux.HandleFunc("GET /api/rollout/events", func(w http.ResponseWriter, r *http.Request) {
		select {
		case j.resultSlots <- struct{}{}:
			defer func() { <-j.resultSlots }()
		case <-r.Context().Done():
			return
		}
		id := r.URL.Query().Get("id")
		if err := rollouts.ValidateID(id); err != nil {
			http.Error(w, "invalid rollout ID", http.StatusBadRequest)
			return
		}
		entry, err := j.source.Detail(r.Context(), id)
		if err != nil {
			writeRemoteReadError(w, fmt.Errorf("read rollout metadata: %w", err))
			return
		}
		if entry.JobID != j.jobID {
			http.Error(w, "rollout does not belong to this job", http.StatusNotFound)
			return
		}
		page, err := j.source.Events(r.Context(), id, r.URL.Query().Get("continuationToken"))
		if err != nil {
			writeRemoteReadError(w, fmt.Errorf("read rollout events: %w", err))
			return
		}
		writeRunJSON(w, page)
	})
	// Capability-specific routes must not fall through to the static file server.
	for _, path := range []string{"/api/rollouts/states", "/api/run/logs"} {
		mux.HandleFunc("GET "+path, http.NotFound)
	}
}

// parseWatched keeps at most one refresh worth of valid rollout IDs.
func parseWatched(raw string) []string {
	var ids []string
	for id := range strings.SplitSeq(raw, ",") {
		if len(ids) == runningPerRefresh {
			break
		}
		if rollouts.ValidateID(id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func writeRemoteReadError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	writeRunJSON(w, map[string]string{"error": err.Error()})
}
