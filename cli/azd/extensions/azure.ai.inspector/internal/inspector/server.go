// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	// Port is the TCP port the inspector UI listens on.
	Port int

	// AgentPort is the localhost port of the agent the inspector targets.
	AgentPort int

	// Logger receives RPC logging. If nil, a default prefixed logger is used.
	Logger *log.Logger

	// SessionID and ConversationID seed the SPA's initial conversation so the
	// inspector continues whatever chat the CLI was using. Empty strings
	// mean "no seed available" — the SPA falls back to a fresh UUID.
	SessionID      string
	ConversationID string

	// SSESink, if non-nil, receives the raw bytes of each proxied SSE
	// response so the caller can render it (e.g. echo to the terminal).
	SSESink func(io.Reader)

	// Silent suppresses terminal output that is useful for standalone
	// inspector runs but noisy when azd ai agent run auto-launches it.
	Silent bool
}

type Server struct {
	cfg      Config
	httpSrv  *http.Server
	upgrader websocket.Upgrader
	logger   *log.Logger
}

func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(log.Writer(), "[inspector] ", log.LstdFlags)
	}
	return &Server{
		cfg:    cfg,
		logger: logger,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				return origin == "http://"+r.Host || origin == "https://"+r.Host
			},
		},
	}
}

func (s *Server) URL() string {
	return fmt.Sprintf("http://localhost:%d", s.cfg.Port)
}

// Start serves until ctx is cancelled. If ready is non-nil, it is closed
// once the listener is bound.
func (s *Server) Start(ctx context.Context, ready chan<- struct{}) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/agentdev/ws/rpc", s.handleWS)
	mux.Handle("/", assetsHandler(Assets()))

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind inspector port %d: %w", s.cfg.Port, err)
	}
	if ready != nil {
		close(ready)
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
		<-srvErr
		return nil
	case err := <-srvErr:
		return err
	}
}

// assetsHandler serves embedded SPA assets, falling back to index.html
// for unknown paths so client-side routes resolve.
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
		if _, err := fs.Stat(fsys, strings.TrimPrefix(path, "/")); err != nil {
			serveIndex(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
