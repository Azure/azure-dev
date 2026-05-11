// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Config controls the behavior of the inspector server.
type Config struct {
	// Port is the TCP port the inspector UI listens on.
	Port int

	// AgentPort is the localhost port of the agent the inspector should
	// connect to. It is forwarded to the SPA via the navigateToStep
	// payload that the server sends in response to the setViewReady
	// notification.
	AgentPort int

	// Logger is used for verbose RPC logging. May be nil; when nil, a
	// default logger that routes through the standard log package is used
	// so the extension's --debug handling (setupDebugLogging) controls
	// where output goes.
	Logger *log.Logger
}

// Server hosts the standalone inspector SPA and the JSON-RPC WebSocket
// endpoint the SPA uses to proxy localhost HTTP/SSE/WS calls.
type Server struct {
	cfg      Config
	httpSrv  *http.Server
	upgrader websocket.Upgrader
	logger   *log.Logger
}

// New constructs a Server. It does not yet listen — call Start.
func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(log.Writer(), "[inspector] ", log.LstdFlags)
	}
	return &Server{
		cfg:    cfg,
		logger: logger,
		upgrader: websocket.Upgrader{
			// Inspector is hosted on the same origin it serves from;
			// allow same-origin upgrades and reject everything else.
			// Browsers always include the port in Origin and r.Host always
			// carries the listen port too (we bind 127.0.0.1:<port>), so
			// non-default --inspector-port values match correctly.
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					// Some clients (e.g. tests) omit Origin; allow.
					return true
				}
				return origin == "http://"+r.Host || origin == "https://"+r.Host
			},
		},
	}
}

// URL returns the URL where the inspector UI is reachable. Only valid
// after Start has been called.
func (s *Server) URL() string {
	return fmt.Sprintf("http://localhost:%d", s.cfg.Port)
}

// Start begins listening and serves until ctx is cancelled. It returns
// the first error encountered, or nil on a clean shutdown.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/agentdev/ws/rpc", s.handleWS)
	mux.Handle("/", assetsHandler(Assets()))

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Bind explicitly so we surface "port in use" before the browser opens.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind inspector port %d: %w", s.cfg.Port, err)
	}

	srvErr := make(chan error, 1)
	go func() {
		err := s.httpSrv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
			return
		}
		srvErr <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
		// Drain server goroutine.
		<-srvErr
		return nil
	case err := <-srvErr:
		return err
	}
}

// assetsHandler wraps http.FileServer so that single-page-app routes
// (anything not matching a real asset) fall back to index.html. The
// inspector uses client-side routing, so deep links like /testTool
// must still serve index.html.
//
// http.FileServer's default behavior is to issue 301 redirects for /
// (→ index.html) and for /index.html (→ /). For an SPA we want to
// serve the bytes directly without redirects, so we read index.html
// once at startup and serve it ourselves for the SPA routes; only real
// asset files are delegated to the file server.
func assetsHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))

	indexBytes, indexErr := fs.ReadFile(fsys, "index.html")

	serveIndex := func(w http.ResponseWriter) {
		if indexErr != nil {
			http.Error(w, "index.html missing from embedded assets: "+indexErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexBytes)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "" || path == "/" || path == "/index.html" {
			serveIndex(w)
			return
		}

		// fs.Stat is one syscall; the previous Open+Close pair was two.
		// On any miss, fall back to the SPA shell so client-side routes
		// resolve. fs.FS paths are not rooted at "/", so trim the prefix.
		if _, err := fs.Stat(fsys, strings.TrimPrefix(path, "/")); err != nil {
			serveIndex(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// Compile-time check that the embedded FS exposes index.html. Without
// this, a bad build would only fail at request time.
var _ = func() error {
	_, err := fs.Stat(Assets(), "index.html")
	return err
}
