// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package monitor serves a read-only browser view of a rollout snapshot.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"azure.ai.rle/internal/rollouts"
	"azure.ai.rle/internal/ui"
)

// Run loads a rollout and serves it until cancellation. Only the reader knows where the data lives.
func Run(
	ctx context.Context,
	reader rollouts.Reader,
	rolloutID string,
	noBrowser bool,
	out, errOut io.Writer,
) error {
	snapshot, err := reader.Get(ctx, rolloutID)
	if err != nil {
		return err
	}
	return serve(ctx, source{snapshot: &snapshot}, noBrowser, out, errOut)
}

// RunJob serves every rollout a training run recorded, so one job opens as a
// browsable set rather than requiring a rollout ID the caller cannot know.
//
// The index is read once, because a finished run does not gain rollouts. Each
// rollout body is fetched only when opened: a run records thousands, and their
// captured responses are far too large to hold at once.
// RunJob serves the rollouts one training job recorded, so any of them can be opened.
//
// The run may still be going, so the list is not read once: jobIndex re-lists
// from where it stopped, and the page polls for what has landed since.
//
// runDir is the local mirror `train --follow` writes (empty when there is
// none). When present the dashboard also shows what the job is -- its model,
// environment, hyperparameters, metrics and log -- instead of only the rollouts
// it has produced so far.
func RunJob(
	ctx context.Context,
	reader rollouts.Reader,
	lister rollouts.Lister,
	jobID string,
	runDir string,
	noBrowser bool,
	out, errOut io.Writer,
) error {
	index := newJobIndex(jobID, lister)
	if _, err := index.fetch(ctx); err != nil {
		return err
	}
	src := source{jobID: jobID, index: index, reader: reader}
	src.probe = newStateProbe(index, reader, stateProbeWorkers)
	if strings.TrimSpace(runDir) != "" {
		src.run = &runArtifacts{dir: runDir}
	}
	return serve(ctx, src, noBrowser, out, errOut)
}

// source is either one saved rollout or a training job's recorded set.
type source struct {
	snapshot *rollouts.Snapshot
	jobID    string
	index    *jobIndex
	reader   rollouts.Reader
	run      *runArtifacts
	probe    *stateProbe
}

func serve(ctx context.Context, src source, noBrowser bool, out, errOut io.Writer) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local monitor: %w", err)
	}
	defer listener.Close()
	handler, err := newHandler(src, listener.Addr().String())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	if src.probe != nil {
		go src.probe.run(ctx)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	link := "http://" + listener.Addr().String() + "/"
	// A single captured rollout and a whole training job are different things to
	// watch, so they are named differently: only a job has steps, metrics and a
	// run log behind it.
	heading := "Rollout monitor"
	if src.jobID != "" {
		// The count is where the list starts, not where it ends: a running job
		// keeps recording, and the page picks the new ones up as they land.
		heading = fmt.Sprintf("Job monitor for %s (%d rollouts so far)", src.jobID, src.index.count())
	}
	if _, err := fmt.Fprintf(out, "%s: %s\nPress Ctrl+C to stop the local monitor.\n", heading, link); err != nil {
		return err
	}
	if !noBrowser {
		if err := ui.OpenBrowser(link); err != nil {
			if _, err := fmt.Fprintln(errOut, "Warning: could not open the browser. Open the monitor link above."); err != nil {
				return err
			}
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("local monitor stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop local monitor: %w", err)
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("local monitor stopped: %w", err)
		}
		return nil
	}
}

func newHandler(src source, host string) (http.Handler, error) {
	var data []byte
	if src.snapshot != nil {
		encoded, err := json.Marshal(src.snapshot)
		if err != nil {
			return nil, fmt.Errorf("encode rollout snapshot: %w", err)
		}
		data = encoded
	}
	assets, err := fs.Sub(Assets, "web")
	if err != nil {
		return nil, fmt.Errorf("load monitor assets: %w", err)
	}
	mux := http.NewServeMux()
	// Absent in single-rollout mode; the page treats 404 as "there is no set to browse".
	//
	// The run may still be going, so this is answered from the live index rather
	// than a list read at startup. `after` carries the last rollout the page
	// already holds, so a poll returns only what has landed since.
	mux.HandleFunc("GET /api/rollouts", func(w http.ResponseWriter, r *http.Request) {
		if src.index == nil {
			http.NotFound(w, r)
			return
		}
		entries, reset := src.index.entriesAfter(r.Context(), r.URL.Query().Get("after"))
		if entries == nil {
			entries = []rollouts.Entry{}
		}
		body := map[string]any{"job_id": src.jobID, "data": entries}
		if reset {
			body["reset"] = true
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			http.Error(w, "could not encode rollout index", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})

	// Whether each rollout ran to completion, which the index cannot say: the
	// harness grades a crashed rollout too, so a failure arrives with a reward
	// and a graph like any other row. Answered from the probe's cache rather
	// than by reading bodies here, so the page's poll stays cheap and a rollout
	// is fetched once for the whole run instead of once per viewer.
	mux.HandleFunc("GET /api/rollouts/states", func(w http.ResponseWriter, r *http.Request) {
		if src.probe == nil {
			http.NotFound(w, r)
			return
		}
		states, known := src.probe.snapshot()
		encoded, err := json.Marshal(map[string]any{
			"job_id": src.jobID,
			"data":   states,
			"known":  known,
			"total":  src.index.count(),
		})
		if err != nil {
			http.Error(w, "could not encode rollout states", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})

	mux.HandleFunc("GET /api/rollout", func(w http.ResponseWriter, r *http.Request) {
		requested := r.URL.Query().Get("id")
		if requested == "" {
			if data == nil {
				http.Error(w, "a rollout id is required", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
			return
		}
		if src.reader == nil {
			http.Error(w, "this monitor serves a single saved rollout", http.StatusNotFound)
			return
		}
		// Serve only rollouts this job recorded, so the page cannot be used to
		// fetch an arbitrary ID through the caller's credentials.
		if src.index == nil || !src.index.contains(r.Context(), requested) {
			http.Error(w, "unknown rollout", http.StatusNotFound)
			return
		}
		snapshot, err := src.reader.Get(r.Context(), requested)
		if err != nil {
			http.Error(w, "could not load rollout: "+err.Error(), http.StatusBadGateway)
			return
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			http.Error(w, "could not encode rollout", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})
	files := http.FileServerFS(assets)
	registerRunRoutes(mux, src)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			info, err := fs.Stat(assets, r.URL.Path[1:])
			if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		// Windows MIME registrations may classify JavaScript as text/plain.
		switch path.Ext(r.URL.Path) {
		case ".js", ".mjs":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; "+
				"connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		if ui.ValidateLoopbackRequest(w, r, host) {
			mux.ServeHTTP(w, r)
		}
	}), nil
}
