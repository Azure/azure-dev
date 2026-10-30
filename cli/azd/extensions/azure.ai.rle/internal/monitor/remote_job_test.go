// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.rle/internal/rollouts"
)

const remoteTestJob = "ftjob-7898670d3ae8453e89e591f9"
const remoteTestRollout = "3c27c30f5fba261c3a7a3e856b4e1388"

var remoteTestTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

type memoryJobSource struct {
	mu sync.Mutex // Point reads run concurrently.

	configCalls, metricCalls, listCalls, detailCalls, resultCalls, statusCalls int

	configErr, resultErr error
	status               string
	entry                JobRollout
	metrics              func(int64, string) (JobPage[json.RawMessage], error)
	rollouts             func(string) (JobPage[JobRollout], error)
	detail               func(string) (JobRollout, error)
	events               func(string, string) (JobPage[json.RawMessage], error)
	eventCalls           int
}

func newMemoryJobSource() *memoryJobSource {
	return &memoryJobSource{
		status: "running",
		entry: JobRollout{
			RolloutID: remoteTestRollout, JobID: remoteTestJob, Status: "running", Success: new(false),
		},
	}
}

func (s *memoryJobSource) Config(context.Context) (json.RawMessage, error) {
	s.configCalls++
	return json.RawMessage(`{"job_id":"` + remoteTestJob + `","max_steps":60}`), s.configErr
}

func (s *memoryJobSource) Metrics(_ context.Context, after int64, token string) (JobPage[json.RawMessage], error) {
	s.metricCalls++
	if s.metrics != nil {
		return s.metrics(after, token)
	}
	return JobPage[json.RawMessage]{Data: []json.RawMessage{json.RawMessage(`{"step":0,"optim/lr":0.0003}`)}}, nil
}

func (s *memoryJobSource) Rollouts(_ context.Context, token string) (JobPage[JobRollout], error) {
	s.listCalls++
	if s.rollouts != nil {
		return s.rollouts(token)
	}
	return JobPage[JobRollout]{Data: []JobRollout{s.entry}}, nil
}

func (s *memoryJobSource) Detail(_ context.Context, id string) (JobRollout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detailCalls++
	if s.detail != nil {
		return s.detail(id)
	}
	return s.entry, nil
}

func (s *memoryJobSource) Result(context.Context, JobRollout) (rollouts.Snapshot, error) {
	s.resultCalls++
	return rollouts.Snapshot{Response: json.RawMessage(`{"rollout_id":"` + remoteTestRollout + `","reward":1}`)}, s.resultErr
}

func (s *memoryJobSource) Status(context.Context) (string, error) {
	s.statusCalls++
	return s.status, nil
}

func (s *memoryJobSource) Events(_ context.Context, id, token string) (JobPage[json.RawMessage], error) {
	s.eventCalls++
	if s.events != nil {
		return s.events(id, token)
	}
	return JobPage[json.RawMessage]{Data: []json.RawMessage{}}, nil
}

func remoteRequest(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, "http://"+testHost+path, nil))
	return recorder
}

func TestRemoteEmptyRolloutListIsArray(t *testing.T) {
	j := newRemoteJob(newMemoryJobSource(), remoteTestJob)
	handler, err := newHandler(source{remote: j, jobID: remoteTestJob}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	response := remoteRequest(t, handler, "GET", "/api/rollouts")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"data":[]`) {
		t.Fatalf("empty rollout list must serialize as []: %d %s", response.Code, response.Body)
	}
}

func TestRemoteRolloutEvents(t *testing.T) {
	for _, status := range []string{"running", "failed", "completed", "cancelled", "unknown"} {
		t.Run(status, func(t *testing.T) {
			reader := newMemoryJobSource()
			reader.entry.Status = status
			reader.events = func(id, token string) (JobPage[json.RawMessage], error) {
				if id != remoteTestRollout || token != "opaque+/=" {
					t.Fatalf("unexpected events request id=%s token=%s", id, token)
				}
				return JobPage[json.RawMessage]{
					Data: []json.RawMessage{json.RawMessage(`{"sequence":3,"phase":"execute","message":"Working"}`)},
					Next: "next",
				}, nil
			}
			handler, err := newHandler(source{remote: newRemoteJob(reader, remoteTestJob)}, testHost)
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/rollout/events?id=" + remoteTestRollout + "&continuationToken=opaque%2B%2F%3D"
			response := remoteRequest(t, handler, "GET", path)
			if response.Code != http.StatusOK || reader.eventCalls != 1 || reader.resultCalls != 0 ||
				!strings.Contains(response.Body.String(), `"sequence":3`) ||
				!strings.Contains(response.Body.String(), `"nextContinuationToken":"next"`) {
				t.Fatalf("events failed: %d %s", response.Code, response.Body)
			}
			reader.events = func(string, string) (JobPage[json.RawMessage], error) {
				return JobPage[json.RawMessage]{}, &ReadError{
					Status: 429, Code: "TooManyRequests", Operation: "event-operation",
				}
			}
			response = remoteRequest(t, handler, "GET", path)
			if response.Code != http.StatusBadGateway ||
				!strings.Contains(response.Body.String(), "event-operation") {
				t.Fatalf("event failure not surfaced: %d %s", response.Code, response.Body)
			}
			reader.entry.JobID = "another-job"
			response = remoteRequest(t, handler, "GET", path)
			if response.Code != http.StatusNotFound || reader.eventCalls != 2 {
				t.Fatal("cross-job events exposed")
			}
			response = remoteRequest(t, handler, "GET", "/api/rollout/events?id=invalid")
			if response.Code != http.StatusBadRequest || reader.detailCalls != 3 || reader.eventCalls != 2 {
				t.Fatal("invalid ID reached the event source")
			}
			reader.detail = func(string) (JobRollout, error) {
				return JobRollout{}, &ReadError{Status: 403, Code: "Forbidden"}
			}
			response = remoteRequest(t, handler, "GET", path)
			if response.Code != http.StatusBadGateway || reader.eventCalls != 2 ||
				!strings.Contains(response.Body.String(), "Forbidden") {
				t.Fatal("metadata error did not prevent event reads")
			}
		})
	}
}

func TestRemoteMonitorPollsSummariesButNeverResultsUntilOpened(t *testing.T) {
	reader := newMemoryJobSource()
	j := newRemoteJob(reader, remoteTestJob)
	handler, err := newHandler(source{remote: j, jobID: remoteTestJob}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	now := remoteTestTime
	j.refresh(t.Context(), now, false)
	for range 3 {
		for _, path := range []string{"/api/run", "/api/run/metrics", "/api/rollouts"} {
			if response := remoteRequest(t, handler, "GET", path); response.Code != 200 {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body)
			}
		}
	}
	if reader.resultCalls != 0 || reader.eventCalls != 0 ||
		reader.configCalls != 1 || reader.metricCalls != 1 || reader.detailCalls != 0 {
		t.Fatalf("browser reads triggered remote calls: %+v", reader)
	}
	// Neither discovery nor the unwatched running refresh is due after 15s; the
	// next full scan a minute later picks up the status change.
	reader.entry.Status, reader.entry.ResultAvailable = "completed", true
	reader.entry.Reward = new(0.5)
	j.refresh(t.Context(), now.Add(15*time.Second), false)
	if body := remoteRequest(t, handler, "GET", "/api/rollouts").Body.String(); !strings.Contains(body, "running") {
		t.Fatal(body)
	}
	j.refresh(t.Context(), now.Add(time.Minute), false)
	response := remoteRequest(t, handler, "GET", "/api/rollouts")
	if !strings.Contains(response.Body.String(), `"status":"completed"`) ||
		!strings.Contains(response.Body.String(), `"success":false`) {
		t.Fatal(response.Body.String())
	}
	if reader.resultCalls != 0 || reader.detailCalls != 0 {
		t.Fatalf("polling read results or point-read a row the scan just returned: %+v", reader)
	}
	j.refresh(t.Context(), now.Add(2*time.Minute), false)
	if reader.detailCalls != 0 {
		t.Fatal("completed rollout was point-read")
	}
	response = remoteRequest(t, handler, "GET", "/api/rollout?id="+remoteTestRollout)
	if response.Code != 200 || reader.resultCalls != 1 || reader.detailCalls != 1 {
		t.Fatalf("click failed: %d %s", response.Code, response.Body)
	}
	reader.resultErr = &ReadError{Status: 404, Code: "RolloutResultNotFound"}
	response = remoteRequest(t, handler, "GET", "/api/rollout?id="+remoteTestRollout)
	if !strings.Contains(response.Body.String(), `"unavailable":true`) {
		t.Fatal(response.Body.String())
	}
	reader.entry.JobID = "another-job"
	response = remoteRequest(t, handler, "GET", "/api/rollout?id="+remoteTestRollout)
	if response.Code != http.StatusNotFound || reader.resultCalls != 2 {
		t.Fatal("cross-job result exposed")
	}
}

func TestRemoteMonitorRegistrationAndSchedule(t *testing.T) {
	reader := newMemoryJobSource()
	reader.configErr = &ReadError{Status: 404, Code: "JobNotFound"}
	j := newRemoteJob(reader, remoteTestJob)
	now := remoteTestTime
	j.refresh(t.Context(), now, false)
	if j.config != nil || !strings.Contains(j.states["config"].Error, "Waiting for job registration") {
		t.Fatal("registration delay not represented")
	}
	j.refresh(t.Context(), now.Add(time.Second), false)
	if reader.configCalls != 1 || reader.metricCalls != 1 {
		t.Fatal("polled before due")
	}
	reader.configErr = nil
	j.refresh(t.Context(), now.Add(5*time.Second), false)
	if reader.configCalls != 2 || reader.metricCalls != 1 || reader.statusCalls != 1 {
		t.Fatalf("unexpected cadence: %+v", reader)
	}
	j.refresh(t.Context(), now.Add(10*time.Second), false)
	if reader.metricCalls != 2 || reader.listCalls != 1 || reader.statusCalls != 1 {
		t.Fatalf("unexpected cadence: %+v", reader)
	}
	j.refresh(t.Context(), now.Add(time.Minute), false)
	if reader.configCalls != 2 || reader.statusCalls != 2 || reader.listCalls != 2 || reader.detailCalls != 0 {
		t.Fatalf("unexpected cadence: %+v", reader)
	}
}

func TestRemoteMetricPagingKeepsWatermarkFixedAndRecovers(t *testing.T) {
	reader := newMemoryJobSource()
	fail := true
	reader.metrics = func(after int64, token string) (JobPage[json.RawMessage], error) {
		if after != -1 {
			t.Fatalf("watermark moved inside cycle: %d", after)
		}
		if token == "" {
			return JobPage[json.RawMessage]{Data: []json.RawMessage{json.RawMessage(`{"step":3}`)}, Next: "next"}, nil
		}
		if fail {
			return JobPage[json.RawMessage]{}, errors.New("temporary read failure")
		}
		return JobPage[json.RawMessage]{Data: []json.RawMessage{json.RawMessage(`{"step":7}`)}}, nil
	}
	j := newRemoteJob(reader, remoteTestJob)
	now := remoteTestTime
	if err := j.readMetrics(t.Context(), now); err == nil {
		t.Fatal("expected failure")
	}
	if j.metricCursor.watermark != -1 || len(j.metrics) != 1 {
		t.Fatal("cursor committed early")
	}
	fail = false
	if err := j.readMetrics(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if j.metricCursor.watermark != 7 || len(j.metrics) != 2 {
		t.Fatal("retry skipped or duplicated rows")
	}
	reader.metrics = func(after int64, token string) (JobPage[json.RawMessage], error) {
		if after != -1 {
			t.Fatal("reconciliation must include late lower steps")
		}
		return JobPage[json.RawMessage]{Data: []json.RawMessage{json.RawMessage(`{"step":1}`)}}, nil
	}
	if err := j.readMetrics(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(j.metrics) != 3 || j.metricCursor.watermark != 7 {
		t.Fatal("late step lost")
	}
}

func testRollout(index int, created time.Time) JobRollout {
	return JobRollout{
		RolloutID: fmt.Sprintf("%032x", index), JobID: remoteTestJob, Status: "completed", CreatedAt: created,
	}
}

func TestRemoteRolloutScansStartWithoutTokenAndUpsert(t *testing.T) {
	reader := newMemoryJobSource()
	older, newer := testRollout(9, remoteTestTime), testRollout(1, remoteTestTime.Add(time.Second))
	newer.Status = "running" // Rows without sequence_id are accepted.
	var tokens []string
	reader.rollouts = func(token string) (JobPage[JobRollout], error) {
		tokens = append(tokens, token)
		switch token {
		case "":
			return JobPage[JobRollout]{Data: []JobRollout{}, Next: "empty"}, nil // Empty page, more to come.
		case "empty":
			return JobPage[JobRollout]{Data: []JobRollout{older}, Next: "second"}, nil
		default:
			return JobPage[JobRollout]{Data: []JobRollout{newer}}, nil
		}
	}
	j := newRemoteJob(reader, remoteTestJob)
	if err := j.readRollouts(t.Context(), remoteTestTime); err != nil {
		t.Fatal(err)
	}
	if len(j.entries) != 2 || j.rolloutScan != nil || j.rolloutsReconciled != remoteTestTime ||
		!slices.Equal(tokens, []string{"", "empty", "second"}) {
		t.Fatalf("scan not completed: %d entries, tokens %q", len(j.entries), tokens)
	}
	if got := j.sortedEntries(); got[0].RolloutID != older.RolloutID {
		t.Fatal("rollouts not ordered by creation time")
	}
	// The next discovery is a new full scan from the first page, and upserts.
	newer.Status, newer.CreatedAt = "completed", time.Time{}
	tokens = nil
	if err := j.readRollouts(t.Context(), remoteTestTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tokens, []string{"", "empty", "second"}) || len(j.entries) != 2 {
		t.Fatalf("discovery did not rescan from the first page: %q", tokens)
	}
	if entry := j.entries[newer.RolloutID]; entry.Status != "completed" || entry.CreatedAt.IsZero() {
		t.Fatalf("upsert lost status or creation time: %+v", entry)
	}
	reader.rollouts = func(string) (JobPage[JobRollout], error) {
		return JobPage[JobRollout]{Data: []JobRollout{}, Next: "repeat"}, nil
	}
	if err := j.readRollouts(t.Context(), time.Now()); err == nil || j.rolloutScan != nil {
		t.Fatal("expected repeated-token error and a reset scan")
	}
	reader.metrics = func(int64, string) (JobPage[json.RawMessage], error) {
		return JobPage[json.RawMessage]{Next: "repeat"}, nil
	}
	if err := j.readMetrics(t.Context(), time.Now()); err == nil {
		t.Fatal("expected repeated-token error")
	}
}

func TestRemoteUnfinishedRolloutScanContinuesNextTick(t *testing.T) {
	reader := newMemoryJobSource()
	reader.rollouts = func(token string) (JobPage[JobRollout], error) {
		index, _ := strconv.Atoi(token) // The first page has no token.
		page := JobPage[JobRollout]{Data: []JobRollout{testRollout(index, remoteTestTime)}}
		if index < pagesPerRefresh+4 {
			page.Next = fmt.Sprint(index + 1)
		}
		return page, nil
	}
	j := newRemoteJob(reader, remoteTestJob)
	j.refresh(t.Context(), remoteTestTime, false)
	if reader.listCalls != pagesPerRefresh || j.rolloutScan == nil || j.rolloutScan.token == "" {
		t.Fatalf("first refresh read %d pages", reader.listCalls)
	}
	j.refresh(t.Context(), remoteTestTime.Add(time.Second), false)
	if reader.listCalls != pagesPerRefresh+5 || j.rolloutScan != nil || len(j.entries) != pagesPerRefresh+5 ||
		j.rolloutsReconciled != remoteTestTime {
		t.Fatalf("unfinished scan did not continue on the next tick: %d calls", reader.listCalls)
	}
	j.refresh(t.Context(), remoteTestTime.Add(2*time.Second), false)
	if reader.listCalls != pagesPerRefresh+5 {
		t.Fatal("a finished scan restarted before the discovery interval")
	}
}

func TestRemoteRolloutRejectedTokenRescans(t *testing.T) {
	reader := newMemoryJobSource()
	var tokens []string
	reader.rollouts = func(token string) (JobPage[JobRollout], error) {
		tokens = append(tokens, token)
		if token != "" {
			return JobPage[JobRollout]{}, &ReadError{Status: 400, Code: "InvalidContinuationToken"}
		}
		return JobPage[JobRollout]{Data: []JobRollout{reader.entry}}, nil
	}
	j := newRemoteJob(reader, remoteTestJob)
	j.rolloutScan = &rolloutScan{started: remoteTestTime, token: "stale", seen: map[string]bool{}}
	if err := j.readRollouts(t.Context(), remoteTestTime.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tokens, []string{"stale", ""}) || len(j.entries) != 1 {
		t.Fatalf("rejected token not rescanned: %q", tokens)
	}
	reader.rollouts = func(string) (JobPage[JobRollout], error) {
		return JobPage[JobRollout]{}, &ReadError{Status: 400, Code: "InvalidPagination"}
	}
	if err := j.readRollouts(t.Context(), remoteTestTime.Add(time.Minute)); err == nil {
		t.Fatal("a 400 on the first page must surface, not loop")
	}
}

func TestRemoteRunningRefreshIsBoundedAndKeepsCacheBounds(t *testing.T) {
	reader := newMemoryJobSource()
	j := newRemoteJob(reader, remoteTestJob)
	now := remoteTestTime
	for index := range runningPerRefresh + 10 {
		entry := testRollout(index, now.Add(time.Duration(index)*time.Second))
		entry.Status = "running"
		j.entries[entry.RolloutID] = entry
	}
	done := testRollout(999, now)
	j.entries[done.RolloutID] = done
	gone := fmt.Sprintf("%032x", runningPerRefresh+9)
	reader.detail = func(id string) (JobRollout, error) {
		if id == gone {
			return JobRollout{}, &ReadError{Status: 404, Code: "RolloutNotFound"}
		}
		entry := testRollout(0, time.Time{})
		entry.RolloutID, entry.Status = id, "completed"
		return entry, nil
	}
	if err := j.refreshRunning(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if reader.detailCalls != runningPerRefresh {
		t.Fatalf("point reads not bounded: %d", reader.detailCalls)
	}
	if _, ok := j.entries[gone]; ok {
		t.Fatal("deleted rollout kept")
	}
	if entry := j.entries[fmt.Sprintf("%032x", 10)]; entry.Status != "completed" || entry.CreatedAt.IsZero() {
		t.Fatalf("newest running rows not refreshed with their creation time kept: %+v", entry)
	}
	if j.entries[fmt.Sprintf("%032x", 0)].Status != "running" {
		t.Fatal("oldest running rows should wait for the next refresh")
	}
	reader.detail = func(string) (JobRollout, error) { return JobRollout{JobID: "another-job"}, nil }
	if err := j.refreshRunning(t.Context(), now); err == nil {
		t.Fatal("cross-job metadata accepted")
	}
}

func TestRemoteRunningRefreshPrefersWatchedRows(t *testing.T) {
	reader := newMemoryJobSource()
	j := newRemoteJob(reader, remoteTestJob)
	now := remoteTestTime
	for index := range runningPerRefresh + 10 {
		entry := testRollout(index, now.Add(time.Duration(index)*time.Second))
		entry.Status = "running"
		j.entries[entry.RolloutID] = entry
		j.readAt[entry.RolloutID] = now
	}
	oldest := fmt.Sprintf("%032x", 0)
	handler := http.NewServeMux()
	registerRemoteRoutes(handler, j)
	remoteRequest(t, handler, "GET", "/api/rollouts?watch="+oldest+",not-an-id")
	if len(j.watched) != 1 || j.watched[0] != oldest {
		t.Fatalf("watch list not parsed: %v", j.watched)
	}
	j.watchedAt = now
	var read []string
	reader.detail = func(id string) (JobRollout, error) {
		read = append(read, id)
		entry := testRollout(0, time.Time{})
		entry.RolloutID, entry.Status = id, "running"
		return entry, nil
	}
	if err := j.refreshRunning(t.Context(), now.Add(rolloutWatchedEvery)); err != nil {
		t.Fatal(err)
	}
	if len(read) != 1 || read[0] != oldest {
		t.Fatalf("only the watched row is due after 10s: %v", read)
	}
	read = nil
	j.watchedAt = now.Add(rolloutRunningEvery) // The page keeps naming it.
	if err := j.refreshRunning(t.Context(), now.Add(rolloutRunningEvery)); err != nil {
		t.Fatal(err)
	}
	if len(read) != runningPerRefresh || !slices.Contains(read, oldest) {
		t.Fatalf("watched row not kept ahead of the bound: %d reads", len(read))
	}
	read = nil
	later := now.Add(rolloutRunningEvery + watchExpiry)
	for id := range j.entries {
		j.readAt[id] = later
	}
	if err := j.refreshRunning(t.Context(), later.Add(rolloutWatchedEvery)); err != nil {
		t.Fatal(err)
	}
	if len(read) != 0 {
		t.Fatalf("expired watch still read every 10s: %v", read)
	}
}

func TestRemoteMonitorBackoffAndBoundedMetrics(t *testing.T) {
	j := newRemoteJob(newMemoryJobSource(), remoteTestJob)
	now := remoteTestTime
	calls := 0
	read := func() error {
		calls++
		return &ReadError{Status: 429, RetryAfter: time.Minute}
	}
	j.poll(t.Context(), now, "metrics", 5*time.Second, read)
	j.poll(t.Context(), now.Add(30*time.Second), "metrics", 5*time.Second, read)
	if calls != 1 || j.states["metrics"].Error == "" {
		t.Fatal("Retry-After ignored")
	}
	j.refresh(t.Context(), now.Add(30*time.Second), true)
	j.poll(t.Context(), now.Add(30*time.Second), "metrics", 5*time.Second, read)
	if calls != 1 {
		t.Fatal("manual refresh ignored Retry-After")
	}
	for step := range int64(maxRunMetricLines + 10) {
		j.metrics[step] = json.RawMessage(`{"step":0}`)
	}
	j.trimMetrics()
	if len(j.metrics) != maxRunMetricLines || !j.limited || j.metrics[0] != nil || j.metricCursor.floor != 9 {
		t.Fatal("history not bounded")
	}
}

func TestRemoteRefreshRetriesFailedReads(t *testing.T) {
	j := newRemoteJob(newMemoryJobSource(), remoteTestJob)
	now := remoteTestTime
	calls := 0
	read := func() error {
		calls++
		return errors.New("temporary read failure")
	}
	for range 3 {
		j.poll(t.Context(), now, "metrics", 5*time.Second, read)
		j.mu.Lock()
		state := j.states["metrics"]
		state.Next = state.RetryAt // What a manual refresh does.
		j.states["metrics"] = state
		j.mu.Unlock()
	}
	if calls != 3 {
		t.Fatalf("manual refresh did not retry a failed read: %d calls", calls)
	}
}

func TestRemoteFeatureDisabledStopsPolling(t *testing.T) {
	reader := newMemoryJobSource()
	reader.configErr = &ReadError{Status: 403, Code: "FeatureDisabled"}
	j := newRemoteJob(reader, remoteTestJob)
	now := remoteTestTime
	j.refresh(t.Context(), now, false)
	if j.paused != pausedDisabled || !strings.Contains(j.states["config"].Error, "not enabled") {
		t.Fatalf("feature gate not reported: %q %+v", j.paused, j.states["config"])
	}
	calls := reader.configCalls
	j.refresh(t.Context(), now.Add(time.Hour), false)
	if reader.configCalls != calls {
		t.Fatal("disabled feature was polled again")
	}
	j.refresh(t.Context(), now.Add(time.Hour), true)
	if reader.configCalls == calls {
		t.Fatal("manual refresh did not retry")
	}
}

func TestRemoteRolloutCacheKeepsNewestRows(t *testing.T) {
	reader := newMemoryJobSource()
	reader.rollouts = func(string) (JobPage[JobRollout], error) {
		page := JobPage[JobRollout]{Data: []JobRollout{}}
		for index := range remoteRolloutLimit + 5 {
			page.Data = append(page.Data, testRollout(index, remoteTestTime.Add(time.Duration(index)*time.Second)))
		}
		return page, nil
	}
	j := newRemoteJob(reader, remoteTestJob)
	for _, at := range []time.Duration{0, time.Minute} {
		if err := j.readRollouts(t.Context(), remoteTestTime.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, kept := j.entries[fmt.Sprintf("%032x", 4)]; len(j.entries) != remoteRolloutLimit || !j.limited || kept {
		t.Fatal("rollout cache not bounded to the newest rows")
	}
	if total := j.totalRollouts(); total != remoteRolloutLimit+5 {
		t.Fatalf("total rollouts = %d, want %d", total, remoteRolloutLimit+5)
	}
}

func TestRemoteCompletionPausesAndRefreshResumes(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			reader := newMemoryJobSource()
			reader.status = status
			reader.entry.Status, reader.entry.ResultAvailable = "completed", true
			j := newRemoteJob(reader, remoteTestJob)
			now := remoteTestTime
			j.refresh(t.Context(), now, false)
			if j.terminalAt != now || j.status != status || j.paused != "" {
				t.Fatal("terminal status was not preserved or paused before settling")
			}
			handler, err := newHandler(source{remote: j, jobID: remoteTestJob}, testHost)
			if err != nil {
				t.Fatal(err)
			}
			response := remoteRequest(t, handler, "GET", "/api/run")
			var run struct {
				Run struct {
					Config struct {
						MaxSteps int `json:"max_steps"`
					} `json:"config"`
					Meta struct {
						Status string `json:"status"`
					} `json:"meta"`
				} `json:"run"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil ||
				run.Run.Meta.Status != status || run.Run.Config.MaxSteps != 60 {
				t.Fatalf("dashboard did not preserve root config and raw status: %s err=%v", response.Body, err)
			}
			j.refresh(t.Context(), now.Add(time.Minute), false)
			if j.paused != pausedCompleted || reader.statusCalls != 1 {
				t.Fatal("terminal settling and reconciliation did not pause polling")
			}
			calls := reader.metricCalls
			j.refresh(t.Context(), now.Add(2*time.Minute), false)
			if reader.metricCalls != calls {
				t.Fatal("paused monitor still polled")
			}
			j.refresh(t.Context(), now.Add(2*time.Minute), true)
			if reader.metricCalls == calls || j.paused != "" {
				t.Fatal("manual refresh did not resume")
			}
		})
	}
}

func TestRemoteNonTerminalStatusKeepsPolling(t *testing.T) {
	for _, status := range []string{"running", "queued", "completed", "canceled", "custom_state", "Completed", " completed "} {
		t.Run(status, func(t *testing.T) {
			reader := newMemoryJobSource()
			reader.status = status
			j := newRemoteJob(reader, remoteTestJob)
			j.refresh(t.Context(), remoteTestTime, false)
			j.refresh(t.Context(), remoteTestTime.Add(time.Minute), false)
			if !j.terminalAt.IsZero() || j.paused != "" || j.status != status || reader.statusCalls != 2 {
				t.Fatal("nonterminal status was coerced or stopped polling")
			}
		})
	}
}

func TestRemoteConcurrentBrowserReads(t *testing.T) {
	j := newRemoteJob(newMemoryJobSource(), remoteTestJob)
	handler, err := newHandler(source{remote: j, jobID: remoteTestJob}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 20 {
			j.refresh(t.Context(), time.Now(), true)
		}
	})
	for range 3 {
		wg.Go(func() {
			for range 20 {
				remoteRequest(t, handler, "GET", "/api/run/metrics")
			}
		})
	}
	wg.Wait()
}

func TestRemoteRefreshAnswersAfterForcedCycle(t *testing.T) {
	reader := newMemoryJobSource()
	j := newRemoteJob(reader, remoteTestJob)
	handler := http.NewServeMux()
	registerRemoteRoutes(handler, j)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var refreshed sync.WaitGroup
	refreshed.Go(func() {
		done := <-j.force
		j.refresh(ctx, time.Now(), true)
		close(done)
	})
	response := remoteRequest(t, handler, "POST", "/api/refresh")
	refreshed.Wait()
	if response.Code != http.StatusNoContent {
		t.Fatalf("refresh must answer after its cycle: %d", response.Code)
	}
	if reader.listCalls == 0 || reader.metricCalls == 0 {
		t.Fatalf("forced cycle did not read the service: list=%d metrics=%d", reader.listCalls, reader.metricCalls)
	}
}

func TestRemoteRefreshGivesUpWhenRequestEnds(t *testing.T) {
	j := newRemoteJob(newMemoryJobSource(), remoteTestJob)
	handler := http.NewServeMux()
	registerRemoteRoutes(handler, j)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, "POST", "http://"+testHost+"/api/refresh", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("an unfinished refresh must answer 202: %d", recorder.Code)
	}
}
