// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runDirWith builds a mirror directory holding exactly the named files, so each
// test states the partial state of a run it is about.
func runDirWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func getRun(t *testing.T, dir string, path string) *httptest.ResponseRecorder {
	t.Helper()
	src := source{jobID: "ftjob-1"}
	if dir != "" {
		src.run = &runArtifacts{dir: dir}
	}
	handler, err := newHandler(src, testHost)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "http://"+testHost+path, nil))
	return recorder
}

// A run that has only just started has mirrored some of its files and not
// others. Naming what is absent lets the page say "not yet" for that one panel
// instead of failing the whole view.
func TestRunOverviewNamesTheFilesThatHaveNotLandedYet(t *testing.T) {
	dir := runDirWith(t, map[string]string{
		runMetaFile: `{"job_id":"ftjob-1","rle":{"name":"competitive_intelligence_agent"}}`,
	})

	overview, err := (&runArtifacts{dir: dir}).overview()
	if err != nil {
		t.Fatalf("a run mid-mirror must still describe itself, got %v", err)
	}
	if !strings.Contains(string(overview.Meta), "competitive_intelligence_agent") {
		t.Fatalf("meta = %s, want the mirrored run_meta.json", overview.Meta)
	}
	if len(overview.Config) != 0 {
		t.Fatalf("config = %s, want nothing for a file that is not there", overview.Config)
	}
	if len(overview.Missing) != 1 || overview.Missing[0] != runConfigFile {
		t.Fatalf("missing = %v, want %s named", overview.Missing, runConfigFile)
	}
}

// The mirror copies whole files, but a reader can still arrive between the
// create and the write. That is a moment, not a broken run.
func TestRunOverviewTreatsAHalfWrittenDocumentAsNotYetThere(t *testing.T) {
	dir := runDirWith(t, map[string]string{
		runMetaFile:   `{"job_id":"ftjob-1"}`,
		runConfigFile: `{"learning_rate": 1e-5, "dataset_buil`,
	})

	overview, err := (&runArtifacts{dir: dir}).overview()
	if err != nil {
		t.Fatalf("a truncated document must not fail the overview, got %v", err)
	}
	if len(overview.Config) != 0 {
		t.Fatalf("config = %s, want a truncated document withheld", overview.Config)
	}
	if len(overview.Missing) != 1 || overview.Missing[0] != runConfigFile {
		t.Fatalf("missing = %v, want the truncated file named", overview.Missing)
	}
	if len(overview.Meta) == 0 {
		t.Fatal("the complete document must still be served alongside the truncated one")
	}
}

// metrics.jsonl is appended to while the run goes, so the last line is often
// half written. Dropping it keeps the charts from flickering out at every step
// boundary; the next poll has the finished line.
func TestRunMetricsDropsThePartialTrailingStep(t *testing.T) {
	dir := runDirWith(t, map[string]string{
		runMetricsFile: `{"step":0,"reward":0.72}
{"step":1,"reward":0.78}

{"step":2,"rew`,
	})

	steps, err := (&runArtifacts{dir: dir}).metrics()
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("got %d steps (%v), want the two complete ones", len(steps), steps)
	}
	var last struct {
		Step   int     `json:"step"`
		Reward float64 `json:"reward"`
	}
	if err := json.Unmarshal(steps[1], &last); err != nil {
		t.Fatal(err)
	}
	if last.Step != 1 || last.Reward != 0.78 {
		t.Fatalf("last step = %+v, want step 1 intact", last)
	}
}

// Opening the dashboard before the first step has been written is the normal
// case: a step takes minutes. An empty chart is the right answer, not an error.
func TestRunMetricsEndpointReportsPendingBeforeTheFirstStep(t *testing.T) {
	recorder := getRun(t, runDirWith(t, map[string]string{runMetaFile: `{}`}), "/api/run/metrics")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want a run with no steps yet to be fine",
			recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data    []json.RawMessage `json:"data"`
		Pending bool              `json:"pending"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Pending {
		t.Fatal("want the absent metrics file reported as pending")
	}
	if body.Data == nil {
		t.Fatal("want an empty list rather than null, so the page can render it")
	}
}

// Following a run must cost only what it has written since the last poll, or a
// multi-hour run re-sends its whole log every few seconds.
func TestRunLogTailReturnsOnlyWhatIsNewSinceTheGivenOffset(t *testing.T) {
	dir := runDirWith(t, map[string]string{runLogFile: "step 0 done\n"})
	artifacts := &runArtifacts{dir: dir}

	first, err := artifacts.logTail(0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "step 0 done\n" {
		t.Fatalf("text = %q, want the whole log on a first read", first.Text)
	}

	// dir is derived from t.TempDir and runLogFile is a fixed package constant.
	// #nosec G304 -- the test controls the complete path.
	file, err := os.OpenFile(filepath.Join(dir, runLogFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("step 1 done\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	next, err := artifacts.logTail(first.Offset)
	if err != nil {
		t.Fatal(err)
	}
	if next.Text != "step 1 done\n" {
		t.Fatalf("text = %q, want only the appended line", next.Text)
	}
	if next.Offset != next.Size {
		t.Fatalf("offset %d, size %d: a caught-up reader must be at the end", next.Offset, next.Size)
	}
}

// Re-following a run replaces the mirrored log, leaving a stale offset past the
// new end. Restarting from the tail is what the reader wants; an error is not.
func TestRunLogTailRestartsWhenTheOffsetIsPastTheEnd(t *testing.T) {
	dir := runDirWith(t, map[string]string{runLogFile: "fresh run\n"})

	tail, err := (&runArtifacts{dir: dir}).logTail(9_000_000)
	if err != nil {
		t.Fatalf("a stale offset must not fail the read, got %v", err)
	}
	if tail.Text != "fresh run\n" {
		t.Fatalf("text = %q, want the replaced log read from the start", tail.Text)
	}
}

// A run left going overnight writes a log far larger than anything worth
// holding in memory to render.
func TestRunLogTailCapsAFirstReadToTheEndOfTheLog(t *testing.T) {
	body := strings.Repeat("x", maxRunLogBytes) + "the newest line\n"
	dir := runDirWith(t, map[string]string{runLogFile: body})

	tail, err := (&runArtifacts{dir: dir}).logTail(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Text) > maxRunLogBytes {
		t.Fatalf("returned %d bytes, want no more than %d", len(tail.Text), maxRunLogBytes)
	}
	if !strings.HasSuffix(tail.Text, "the newest line\n") {
		t.Fatal("want the end of the log kept, not the start")
	}
	if tail.Size != int64(len(body)) {
		t.Fatalf("size = %d, want the true file size %d so the page can say how much it skipped",
			tail.Size, len(body))
	}
}

// Monitoring a job that was never followed on this machine is allowed -- the
// rollouts come from the service. The run panels must say plainly that there is
// nothing local, rather than serving the page shell as if it were data.
func TestRunEndpointsAreNotFoundWhenNoRunWasMirrored(t *testing.T) {
	for _, path := range []string{"/api/run", "/api/run/metrics", "/api/run/logs"} {
		t.Run(path, func(t *testing.T) {
			recorder := getRun(t, "", path)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 when the monitor has no local run", recorder.Code)
			}
		})
	}
}

// The rollout list and the run artifacts are separate sources, and a job can
// have one without the other; serving them from one handler must not couple them.
func TestRunOverviewEndpointServesTheJobItWasOpenedFor(t *testing.T) {
	dir := runDirWith(t, map[string]string{
		runMetaFile:   `{"rle":{"name":"competitive_intelligence_agent","version":"1.0.18"}}`,
		runConfigFile: `{"learning_rate":1e-05}`,
	})

	recorder := getRun(t, dir, "/api/run")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body struct {
		JobID string      `json:"job_id"`
		Run   runOverview `json:"run"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.JobID != "ftjob-1" {
		t.Fatalf("job_id = %q, want the job the monitor was opened for", body.JobID)
	}
	if !strings.Contains(string(body.Run.Meta), "1.0.18") ||
		!strings.Contains(string(body.Run.Config), "learning_rate") {
		t.Fatalf("run = %+v, want both mirrored documents", body.Run)
	}
	if len(body.Run.Missing) != 0 {
		t.Fatalf("missing = %v, want nothing named when both files are there", body.Run.Missing)
	}
}
