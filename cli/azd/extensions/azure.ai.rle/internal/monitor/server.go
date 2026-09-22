// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package monitor serves a read-only browser view of a rollout snapshot.
package monitor

import (
	"context"
	"crypto/subtle"
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

const sessionCookie = "azd-rle-monitor-session"

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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start rollout monitor: %w", err)
	}
	defer listener.Close()
	token, err := ui.NewSessionToken()
	if err != nil {
		return err
	}
	handler, err := newHandler(snapshot, listener.Addr().String(), token)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	// The fragment never reaches HTTP access logs. The browser exchanges it for an HttpOnly cookie.
	link := "http://" + listener.Addr().String() + "/"
	if _, err := fmt.Fprintf(out,
		"Rollout monitor: %s\nLocal access code: %s\nPress Ctrl+C to stop the local monitor.\n", link, token,
	); err != nil {
		return err
	}
	if !noBrowser {
		if err := ui.OpenBrowser(link + "#token=" + token); err != nil {
			// Browser errors can contain the launch URL and its local session credential.
			if _, err := fmt.Fprintln(errOut, "Warning: could not open the browser. Open the monitor link above."); err != nil {
				return err
			}
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("rollout monitor stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop rollout monitor: %w", err)
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("rollout monitor stopped: %w", err)
		}
		return nil
	}
}

func newHandler(snapshot rollouts.Snapshot, host, token string) (http.Handler, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode rollout snapshot: %w", err)
	}
	assets, err := fs.Sub(Assets, "web")
	if err != nil {
		return nil, fmt.Errorf("load monitor assets: %w", err)
	}
	mux := http.NewServeMux()
	// Cookies are not port-scoped; give concurrent monitors independent sessions.
	cookieName := sessionCookie + "-" + strings.ReplaceAll(host, ":", "-")
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // Loopback HTTP; token, host and origin checks protect this session.
			Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/rollout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
	files := http.FileServerFS(assets)
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
