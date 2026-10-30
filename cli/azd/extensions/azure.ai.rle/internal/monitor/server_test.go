// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.rle/internal/rollouts"
	"azure.ai.rle/internal/ui"
)

const testHost = "127.0.0.1:12345"

func TestHandlerAuthorizationAndRoutes(t *testing.T) {
	snapshot := rollouts.Snapshot{
		Response: json.RawMessage(`{"rollout_id":"abc","reward":1,"result":"<script>alert(1)</script>"}`),
		Source:   "Saved local result",
	}
	handler, err := newHandler(source{snapshot: &snapshot}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, host, origin string
		status                           int
	}{
		{name: "public shell", method: "GET", path: "/", status: 200},
		{name: "rollout data", method: "GET", path: "/api/rollout", status: 200},
		{name: "foreign host", method: "GET", path: "/api/rollout", host: "evil.test", status: 403},
		{name: "foreign origin", method: "GET", path: "/api/rollout", origin: "https://evil.test", status: 403},
		{name: "read only", method: "POST", path: "/api/rollout", status: 405},
		{name: "unknown route", method: "GET", path: "/api/other", status: 404},
		{name: "no directories", method: "GET", path: "/web/", status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://"+testHost+tc.path, nil)
			if tc.host != "" {
				request.Host = tc.host
			}
			request.Header.Set("Origin", tc.origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" ||
				recorder.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing privacy/security headers")
			}
			if tc.name == "rollout data" {
				var got rollouts.Snapshot
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(got.Response), "reward") {
					t.Fatal("missing rollout response")
				}
			} else if strings.Contains(recorder.Body.String(), "alert(1)") {
				t.Fatal("result leaked outside the data route")
			}
		})
	}
}

func TestModuleAssetsUseJavaScriptMIMEType(t *testing.T) {
	handler, err := newHandler(source{snapshot: &rollouts.Snapshot{Response: json.RawMessage(`{}`)}}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := fs.Glob(Assets, "web/*.mjs")
	if err != nil || len(modules) == 0 {
		t.Fatalf("missing embedded modules: %v", err)
	}
	for _, module := range modules {
		request := httptest.NewRequest("GET", "http://"+testHost+"/"+strings.TrimPrefix(module, "web/"), nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("module %s is not served as JavaScript: %d %v", module, recorder.Code, recorder.Header())
		}
	}
}

type staticReader struct{}

func (staticReader) Get(context.Context, string) (rollouts.Snapshot, error) {
	return rollouts.Snapshot{Response: json.RawMessage(`{"rollout_id":"abc","reward":1}`)}, nil
}

type readyWriter struct {
	ready chan string
}

func (w readyWriter) Write(data []byte) (int, error) {
	w.ready <- string(data)
	return len(data), nil
}

func TestRunServesAndStops(t *testing.T) {
	for _, noBrowser := range []bool{true, false} {
		t.Run(fmt.Sprint(noBrowser), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			oldOpen := ui.OpenBrowser
			t.Cleanup(func() { ui.OpenBrowser = oldOpen })
			opened := make(chan string, 1)
			ui.OpenBrowser = func(link string) error {
				opened <- link
				return errors.New("browser failed with secret-url")
			}
			ready := make(chan string, 1)
			done := make(chan error, 1)
			var diagnostics bytes.Buffer
			go func() { done <- Run(ctx, staticReader{}, "abc", noBrowser, readyWriter{ready}, &diagnostics) }()
			var output string
			select {
			case output = <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("monitor did not start")
			}
			link := strings.TrimPrefix(strings.Split(output, "\n")[0], "Rollout monitor: ")
			if strings.Contains(link, "#") || strings.Contains(link, "?") {
				t.Fatal("printed URL contains unexpected query or fragment")
			}
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(link)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("monitor not responsive: %d", response.StatusCode)
			}
			if !noBrowser {
				select {
				case openedLink := <-opened:
					if openedLink != link {
						t.Fatalf("automatic browser link %q does not match printed link %q", openedLink, link)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("browser not opened")
				}
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("monitor did not stop")
			}
			if noBrowser && len(opened) != 0 {
				t.Fatal("--no-browser opened a browser")
			}
			if strings.Contains(diagnostics.String(), "secret-url") {
				t.Fatal("browser error disclosed its URL")
			}
			if !noBrowser && !strings.Contains(diagnostics.String(), "Warning:") {
				t.Fatal("browser failure was not reported")
			}
		})
	}
}

// stubJobSource models the service's rollout index: rows in the order the run
// recorded them, with `after` seeking past one. Entries can be appended between
// calls, which is what a running job does.
type stubJobSource struct {
	mu       sync.Mutex
	entries  []rollouts.Entry
	requests []string
	listedAt []string
	err      error
}

func (s *stubJobSource) record(entries ...rollouts.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entries...)
}

func (s *stubJobSource) listCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.listedAt...)
}

func (s *stubJobSource) List(_ context.Context, after string) ([]rollouts.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listedAt = append(s.listedAt, after)
	if s.err != nil {
		return nil, s.err
	}
	if after == "" {
		return append([]rollouts.Entry(nil), s.entries...), nil
	}
	for position, entry := range s.entries {
		if entry.RolloutID == after {
			return append([]rollouts.Entry(nil), s.entries[position+1:]...), nil
		}
	}
	return nil, nil
}

func (s *stubJobSource) Get(_ context.Context, rolloutID string) (rollouts.Snapshot, error) {
	s.mu.Lock()
	s.requests = append(s.requests, rolloutID)
	failure := s.err
	s.mu.Unlock()
	if failure != nil {
		return rollouts.Snapshot{}, failure
	}
	return rollouts.Snapshot{
		Response: json.RawMessage(`{"rollout_id":"` + rolloutID + `","reward":1}`),
		Source:   "Training run artifacts",
	}, nil
}

func (s *stubJobSource) fetched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// jobHandler serves the stub through a live index, as the command does.
// The returned index is handed back so a test can age it and force a refresh.
func jobHandlerWithIndex(t *testing.T, stub *stubJobSource) (http.Handler, *jobIndex) {
	t.Helper()
	index := newJobIndex("ftjob-1", stub)
	if _, err := index.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(source{jobID: "ftjob-1", index: index, reader: stub}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	return handler, index
}

func jobHandler(t *testing.T, stub *stubJobSource) http.Handler {
	t.Helper()
	handler, _ := jobHandlerWithIndex(t, stub)
	return handler
}

// age makes the index stale so the next request refreshes, without a test
// having to wait out the refresh interval.
func age(index *jobIndex) {
	index.mu.Lock()
	defer index.mu.Unlock()
	index.attempted = time.Time{}
}

func jobRequest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("GET", "http://"+testHost+path, nil)
	request.Host = testHost
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestJobModeListsAndFetchesOnDemand(t *testing.T) {
	success := true
	reward := 0.75
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1, Split: "validation",
			Reward: &reward, Success: &success},
		{RolloutID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 2, Split: "train"},
	}}
	handler := jobHandler(t, stub)

	index := jobRequest(t, handler, "/api/rollouts")
	if index.Code != 200 {
		t.Fatalf("list status = %d, want 200", index.Code)
	}
	var listed struct {
		JobID string           `json:"job_id"`
		Data  []rollouts.Entry `json:"data"`
	}
	if err := json.Unmarshal(index.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.JobID != "ftjob-1" || len(listed.Data) != 2 {
		t.Fatalf("list = %+v, want job ftjob-1 with 2 entries", listed)
	}
	// The index must stay a summary: a run records thousands of large responses.
	if len(stub.fetched()) != 0 {
		t.Fatalf("listing fetched %v, want no rollout bodies", stub.fetched())
	}

	body := jobRequest(t, handler, "/api/rollout?id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if body.Code != 200 {
		t.Fatalf("fetch status = %d, want 200", body.Code)
	}
	if opened := stub.fetched(); len(opened) != 1 || opened[0] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("fetched %v, want the opened rollout only", opened)
	}
}

func TestJobModeRejectsRolloutsOutsideTheJob(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	handler := jobHandler(t, stub)
	response := jobRequest(t, handler, "/api/rollout?id=cccccccccccccccccccccccccccccccc")
	if response.Code != 404 {
		t.Fatalf("status = %d, want 404 for a rollout this job did not record", response.Code)
	}
	if len(stub.fetched()) != 0 {
		t.Fatalf("fetched %v, want no call for an unlisted rollout", stub.fetched())
	}
}

func TestJobModeRequiresAnID(t *testing.T) {
	handler := jobHandler(t, &stubJobSource{entries: []rollouts.Entry{}})
	if response := jobRequest(t, handler, "/api/rollout"); response.Code != 400 {
		t.Fatalf("status = %d, want 400 when no rollout is named", response.Code)
	}
}

func TestSingleRolloutModeHasNoIndex(t *testing.T) {
	handler, err := newHandler(source{snapshot: &rollouts.Snapshot{Response: json.RawMessage(`{}`)}}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	// The page reads 404 as "there is no set to browse" and shows the saved rollout.
	if response := jobRequest(t, handler, "/api/rollouts"); response.Code != 404 {
		t.Fatalf("status = %d, want 404 without a job", response.Code)
	}
	if response := jobRequest(t, handler, "/api/rollout"); response.Code != 200 {
		t.Fatalf("status = %d, want the saved rollout", response.Code)
	}
}

// A single captured rollout and a whole training job are different things to
// watch, and the printed name is how the user tells which one is in front of
// them. Only a job has steps, metrics and a run log behind it.
func TestJobModeIsNamedAJobMonitorAndRolloutModeIsNot(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- RunJob(ctx, stub, stub, "ftjob-77", "", true, readyWriter{ready}, io.Discard) }()

	var output string
	select {
	case output = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("job monitor did not start")
	}
	if !strings.HasPrefix(output, "Job monitor for ftjob-77 (1 rollouts so far): ") {
		t.Fatalf("heading = %q, want it to name the job monitor and its job", output)
	}
	if strings.Contains(output, "Rollout monitor") {
		t.Fatalf("heading = %q, want a job monitor not a rollout monitor", output)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunJobSurfacesListFailure(t *testing.T) {
	stub := &stubJobSource{err: errors.New("service unavailable")}
	err := RunJob(context.Background(), stub, stub, "ftjob-1", "", true, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "service unavailable") {
		t.Fatalf("err = %v, want the listing failure", err)
	}
}

// A run records rollouts for as long as it lasts, so a list read once would
// stop at whatever had landed when the dashboard opened.
func TestJobModeListsRolloutsRecordedAfterTheDashboardOpened(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler, index := jobHandlerWithIndex(t, stub)

	stub.record(rollouts.Entry{RolloutID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 2})
	age(index)

	var listed struct {
		Data  []rollouts.Entry `json:"data"`
		Reset bool             `json:"reset"`
	}
	response := jobRequest(t, handler, "/api/rollouts")
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("listed %d rollouts, want the one recorded after the dashboard opened too", len(listed.Data))
	}
}

// Polling asks only for what is new, so the cost of a poll does not grow with
// the length of the run.
func TestJobModePollReturnsOnlyWhatIsNew(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler, index := jobHandlerWithIndex(t, stub)

	stub.record(rollouts.Entry{RolloutID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 2})
	age(index)

	var listed struct {
		Data  []rollouts.Entry `json:"data"`
		Reset bool             `json:"reset"`
	}
	response := jobRequest(t, handler, "/api/rollouts?after=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].RolloutID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("poll returned %+v, want only the new rollout", listed.Data)
	}
	if listed.Reset {
		t.Fatal("reset = true, want false: the caller's position was recognized")
	}
	// The trip to the service must also be incremental, not a re-read of the run.
	calls := stub.listCalls()
	if len(calls) != 2 || calls[1] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("service listed at %v, want the second call to resume after the first rollout", calls)
	}
}

// An unrecognized position is answered with the whole list, and says so, or the
// page would append a second copy of every rollout it already shows.
func TestJobModeSignalsAResyncForAnUnknownPosition(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler := jobHandler(t, stub)

	var listed struct {
		Data  []rollouts.Entry `json:"data"`
		Reset bool             `json:"reset"`
	}
	response := jobRequest(t, handler, "/api/rollouts?after=cccccccccccccccccccccccccccccccc")
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if !listed.Reset {
		t.Fatal("reset = false, want true so the page replaces rather than appends")
	}
	if len(listed.Data) != 1 {
		t.Fatalf("listed %d rollouts, want the whole list", len(listed.Data))
	}
}

// The page can show a rollout the index gained on its last poll, so refusing to
// open one merely because an older list did not have it would be wrong.
func TestJobModeOpensARolloutRecordedSinceTheLastRefresh(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler := jobHandler(t, stub)

	stub.record(rollouts.Entry{RolloutID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 2})

	response := jobRequest(t, handler, "/api/rollout?id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if response.Code != 200 {
		t.Fatalf("status = %d, want 200 for a rollout recorded since the last refresh", response.Code)
	}
}

// The guard is the reason the page cannot be used to read an arbitrary rollout
// through the caller's credentials, so a refresh must not weaken it.
func TestJobModeStillRejectsAnUnrecordedRolloutAfterRefreshing(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler := jobHandler(t, stub)

	stub.record(rollouts.Entry{RolloutID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Sequence: 2})

	response := jobRequest(t, handler, "/api/rollout?id=cccccccccccccccccccccccccccccccc")
	if response.Code != 404 {
		t.Fatalf("status = %d, want 404: refreshing must not admit a rollout the job never recorded", response.Code)
	}
	if len(stub.fetched()) != 0 {
		t.Fatalf("fetched %v, want no call for an unrecorded rollout", stub.fetched())
	}
}

// A run in progress may have recorded nothing yet, which is an empty dashboard
// rather than a failure.
func TestJobModeServesARunWithNoRolloutsYet(t *testing.T) {
	handler := jobHandler(t, &stubJobSource{})
	response := jobRequest(t, handler, "/api/rollouts")
	if response.Code != 200 {
		t.Fatalf("status = %d, want 200 for a run that has not recorded a rollout yet", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"data":[]`) {
		t.Fatalf("body = %s, want an empty list", response.Body.String())
	}
}

// A poll that fails must not blank a list the user is reading.
func TestJobModeKeepsListedRolloutsWhenARefreshFails(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler, index := jobHandlerWithIndex(t, stub)

	stub.mu.Lock()
	stub.err = errors.New("service unavailable")
	stub.mu.Unlock()
	age(index)

	response := jobRequest(t, handler, "/api/rollouts")
	if response.Code != 200 {
		t.Fatalf("status = %d, want 200: a failed refresh should not fail the request", response.Code)
	}
	var listed struct {
		Data []rollouts.Entry `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("listed %d rollouts, want the one already known", len(listed.Data))
	}
}

// Without a floor, every request the page makes would be a call to the service.
func TestJobIndexDoesNotRefreshOnEveryRequest(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{
		{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Sequence: 1},
	}}
	handler := jobHandler(t, stub)

	for range 5 {
		jobRequest(t, handler, "/api/rollouts")
	}
	if calls := stub.listCalls(); len(calls) != 1 {
		t.Fatalf("listed %d times, want only the load at startup within the refresh interval", len(calls))
	}
}

// probeHandler wires a handler with the state probe the live monitor runs, so
// a test can exercise the states endpoint end to end.
func probeHandler(t *testing.T, stub *stubJobSource) (http.Handler, *stateProbe) {
	t.Helper()
	index := newJobIndex("ftjob-1", stub)
	if _, err := index.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	probe := newStateProbe(index, stub, 1)
	handler, err := newHandler(source{jobID: "ftjob-1", index: index, reader: stub, probe: probe}, testHost)
	if err != nil {
		t.Fatal(err)
	}
	return handler, probe
}

// The page names the rollouts it is displaying so they are classified first.
// The sweep runs oldest first and a viewer reads a live run newest first, so
// without this the column stays empty exactly where someone is looking.
func TestJobModeClassifiesTheRolloutsThePageIsShowingFirst(t *testing.T) {
	entries := make([]rollouts.Entry, 0, 6)
	for i := range 6 {
		entries = append(entries, rollouts.Entry{RolloutID: fmt.Sprintf("%032d", i), Sequence: i})
	}
	stub := &stubJobSource{entries: entries}
	handler, probe := probeHandler(t, stub)

	newest := fmt.Sprintf("%032d", 5)
	if response := jobRequest(t, handler, "/api/rollouts/states?want="+newest); response.Code != 200 {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if added := probe.fill(context.Background(), 1); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	states, _ := probe.snapshot()
	if _, ok := states[newest]; !ok {
		t.Fatalf("states = %v, want the requested rollout classified first", states)
	}
}

// `want` names rollouts for the probe to read with the operator's credentials,
// so it is held to the same rule as opening one: only what this job recorded.
func TestJobModeIgnoresRequestedRolloutsOutsideTheJob(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	handler, probe := probeHandler(t, stub)

	foreign := "cccccccccccccccccccccccccccccccc"
	if response := jobRequest(t, handler, "/api/rollouts/states?want="+foreign); response.Code != 200 {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	probe.fill(context.Background(), 12)
	for _, id := range stub.fetched() {
		if id == foreign {
			t.Fatalf("fetched %q, a rollout this job did not record", foreign)
		}
	}
}

func TestJobModeServesStatesWithoutARequestList(t *testing.T) {
	stub := &stubJobSource{entries: []rollouts.Entry{{RolloutID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	handler, probe := probeHandler(t, stub)
	probe.fill(context.Background(), 12)

	response := jobRequest(t, handler, "/api/rollouts/states")
	if response.Code != 200 {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body struct {
		Data  map[string]executionState `json:"data"`
		Known int                       `json:"known"`
		Total int                       `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Known != 1 || body.Total != 1 || len(body.Data) != 1 {
		t.Fatalf("body = %+v, want 1 known of 1", body)
	}
}
