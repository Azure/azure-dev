// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Artifact file names the mirror writes. The set is closed on purpose: the
// monitor serves a directory the user owns, and an open-ended reader there
// would turn the dashboard into a file browser for anything that landed in it.
const (
	runMetaFile    = "run_meta.json"
	runConfigFile  = "config.json"
	runMetricsFile = "metrics.jsonl"
	runLogFile     = "logs.log"
)

// Reading caps. A long run's log grows without bound and metrics.jsonl gains a
// line per step; neither should be able to exhaust the monitor's memory because
// someone left a tab open.
const (
	maxRunDocumentBytes = 4 << 20   // run_meta.json / config.json
	maxRunMetricLines   = 5000      // steps held for the charts
	maxRunLogBytes      = 256 << 10 // log bytes returned per request
)

// runArtifacts reads the run files that `train` mirrors by default into
// <logs root>/rle-harness/<job id> (skipped entirely with --no-follow).
//
// Every read goes to disk. The run is usually still going, so the answer to
// "what does this job look like now" changes between requests, and a cache
// would only serve the moment the dashboard first opened.
type runArtifacts struct {
	dir string
}

// runOverview is the job's identity and settings: what the dashboard shows
// above the rollouts so a run explains itself without the submitting terminal.
type runOverview struct {
	Directory string          `json:"directory"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	Config    json.RawMessage `json:"config,omitempty"`
	Missing   []string        `json:"missing,omitempty"`
}

// overview reads run_meta.json and config.json. A file that has not been
// mirrored yet is named in Missing rather than failing the request: the stream
// delivers them in its own order, and the page is expected to open before they
// all arrive.
func (r *runArtifacts) overview() (*runOverview, error) {
	overview := &runOverview{Directory: r.dir}
	for _, item := range []struct {
		name   string
		target *json.RawMessage
	}{
		{runMetaFile, &overview.Meta},
		{runConfigFile, &overview.Config},
	} {
		raw, err := r.readDocument(item.name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			overview.Missing = append(overview.Missing, item.name)
			continue
		case err != nil:
			return nil, err
		}
		if !json.Valid(raw) {
			// A document caught mid-write is not an error worth failing on; the
			// next poll gets the finished one.
			overview.Missing = append(overview.Missing, item.name)
			continue
		}
		*item.target = json.RawMessage(raw)
	}
	return overview, nil
}

// metrics parses metrics.jsonl, one object per training step.
//
// The file is appended to while the run goes, so the final line may be half
// written. That line is dropped rather than treated as corruption: it is
// complete a moment later, and refusing to render the run until then would
// make the charts flicker out on every step boundary.
func (r *runArtifacts) metrics() ([]json.RawMessage, error) {
	file, err := os.Open(r.path(runMetricsFile))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var steps []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxRunDocumentBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			continue
		}
		steps = append(steps, json.RawMessage(line))
		if len(steps) > maxRunMetricLines {
			steps = steps[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", runMetricsFile, err)
	}
	return steps, nil
}

// runLog is a window onto logs.log plus where to resume from.
type runLog struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
}

// logTail returns the log from offset, or the last maxRunLogBytes when the
// caller has no offset yet. The offset it returns is where the next poll should
// start, so following a run costs only the bytes it has written since.
//
// A truncated or replaced file (offset past the end) restarts from the tail
// rather than erroring: re-following a run is a normal thing to do.
func (r *runArtifacts) logTail(offset int64) (*runLog, error) {
	file, err := os.Open(r.path(runLogFile))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", runLogFile, err)
	}
	size := info.Size()
	if offset < 0 || offset > size {
		offset = 0
	}
	if offset == 0 && size > maxRunLogBytes {
		offset = size - maxRunLogBytes
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek %s: %w", runLogFile, err)
	}
	remaining := min(size-offset, maxRunLogBytes)
	buffer := make([]byte, remaining)
	read, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read %s: %w", runLogFile, err)
	}
	return &runLog{Text: string(buffer[:read]), Offset: offset + int64(read), Size: size}, nil
}

func (r *runArtifacts) readDocument(name string) ([]byte, error) {
	file, err := os.Open(r.path(name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRunDocumentBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return raw, nil
}

func (r *runArtifacts) path(name string) string {
	return filepath.Join(r.dir, name)
}

// registerRunRoutes adds the run-artifact endpoints. They are registered even
// when the monitor has no run directory so the page gets an explicit 404 --
// "this monitor has no local run" -- rather than the static handler's index.
func registerRunRoutes(mux *http.ServeMux, src source) {
	// The overview is what the job is, as opposed to what it has produced.
	mux.HandleFunc("GET /api/run", func(w http.ResponseWriter, r *http.Request) {
		if src.run == nil {
			http.NotFound(w, r)
			return
		}
		overview, err := src.run.overview()
		if err != nil {
			http.Error(w, "could not read the run directory: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeRunJSON(w, map[string]any{"job_id": src.jobID, "run": overview})
	})

	// Steps land one at a time over a run that lasts hours, so an absent or
	// empty file is the normal early state, not a failure.
	mux.HandleFunc("GET /api/run/metrics", func(w http.ResponseWriter, r *http.Request) {
		if src.run == nil {
			http.NotFound(w, r)
			return
		}
		steps, err := src.run.metrics()
		if errors.Is(err, fs.ErrNotExist) {
			writeRunJSON(w, map[string]any{"job_id": src.jobID, "data": []json.RawMessage{}, "pending": true})
			return
		}
		if err != nil {
			http.Error(w, "could not read run metrics: "+err.Error(), http.StatusBadGateway)
			return
		}
		if steps == nil {
			steps = []json.RawMessage{}
		}
		writeRunJSON(w, map[string]any{"job_id": src.jobID, "data": steps})
	})

	mux.HandleFunc("GET /api/run/logs", func(w http.ResponseWriter, r *http.Request) {
		if src.run == nil {
			http.NotFound(w, r)
			return
		}
		// A bad offset is treated as "start from the tail" rather than a client
		// error: the page recovers by itself on the next poll either way.
		offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		tail, err := src.run.logTail(offset)
		if errors.Is(err, fs.ErrNotExist) {
			writeRunJSON(w, map[string]any{"text": "", "offset": 0, "size": 0, "pending": true})
			return
		}
		if err != nil {
			http.Error(w, "could not read the run log: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeRunJSON(w, tail)
	})
}

func writeRunJSON(w http.ResponseWriter, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "could not encode the run document", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}
