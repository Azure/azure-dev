// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"azure.ai.rle/internal/project"
	"azure.ai.rle/internal/ui"
)

// playgroundSessionCookie names the loopback session cookie that authorizes requests to
// the local playground UI proxy started by `invoke` (remote) and `run` (local).
const playgroundSessionCookie = "azd-rle-playground-session"

func playgroundURLWithAuthorizationProvider(
	ctx context.Context,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
	runtimeSessions ...*project.WebSocketRuntimeSession,
) (string, func(), error) {
	hasSandboxWeb, err := sandboxHasWebInterface(ctx, sandboxUrl, authorizationProvider)
	if err != nil {
		return "", func() {}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}
	sessionToken, err := newPlaygroundSessionToken()
	if err != nil {
		_ = listener.Close()
		return "", func() {}, err
	}

	server := &http.Server{
		Handler: remotePlaygroundHandler(
			strings.TrimRight(sandboxUrl, "/"),
			authorizationProvider,
			listener.Addr().String(),
			sessionToken,
			hasSandboxWeb,
			runtimeSessions...,
		),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		// The shell remains usable if the optional local UI proxy exits.
		_ = server.Serve(listener)
	}()

	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	return "http://" + listener.Addr().String() + "/web?token=" + url.QueryEscape(sessionToken), stop, nil
}

func remotePlaygroundHandler(
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
	expectedHost string,
	sessionToken string,
	hasSandboxWeb bool,
	runtimeSessions ...*project.WebSocketRuntimeSession,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !validateLoopbackPlaygroundRequest(w, r, expectedHost) {
			return
		}
		if !authorizeLoopbackPlaygroundRequest(w, r, sessionToken) {
			return
		}
		if !hasSandboxWeb && (r.URL.Path == "/" || r.URL.Path == "/web") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, ui.RemotePlaygroundHTML)
			return
		}
		if hasSandboxWeb {
			proxySandboxWeb(w, r, sandboxUrl, authorizationProvider)
			return
		}
		proxyOpenEnvToSandbox(w, r, sandboxUrl, authorizationProvider, runtimeSessions...)
	})
	return mux
}

func sandboxHasWebInterface(
	ctx context.Context,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
) (bool, error) {
	webUrl, err := project.RuntimeOperationURL(sandboxUrl, "web")
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, webUrl, nil)
	if err != nil {
		return false, err
	}
	if authorizationProvider != nil {
		authorization, err := authorizationProvider(ctx)
		if err != nil {
			return false, fmt.Errorf("authenticate to environment web interface: %w", err)
		}
		request.Header.Set("Authorization", authorization)
	}
	response, err := project.HTTPClient(10).Do(request) //nolint:gosec // The active sandbox URL is validated by the caller.
	if err != nil {
		return false, fmt.Errorf("probe environment web interface: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("probe environment web interface: HTTP %d", response.StatusCode)
	}
	return true, nil
}

func proxySandboxWeb(
	w http.ResponseWriter,
	r *http.Request,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
) {
	target, err := url.Parse(sandboxUrl)
	if err != nil {
		http.Error(w, "invalid environment web interface URL", http.StatusBadGateway)
		return
	}
	if authorizationProvider != nil {
		authorization, err := authorizationProvider(r.Context())
		if err != nil {
			http.Error(w, "failed to authenticate to environment web interface", http.StatusBadGateway)
			return
		}
		r.Header.Set("Authorization", authorization)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		cookies := request.Cookies()
		request.Header.Del("Cookie")
		for _, cookie := range cookies {
			if cookie.Name != playgroundSessionCookie {
				request.AddCookie(cookie)
			}
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, fmt.Sprintf("environment web interface proxy failed: %v", err), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func validateLoopbackPlaygroundRequest(w http.ResponseWriter, r *http.Request, expectedHost string) bool {
	if !strings.EqualFold(r.Host, expectedHost) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	originUrl, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(originUrl.Scheme, "http") || !strings.EqualFold(originUrl.Host, expectedHost) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return false
	}
	return true
}

func authorizeLoopbackPlaygroundRequest(w http.ResponseWriter, r *http.Request, sessionToken string) bool {
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/web") {
		if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
			if token != sessionToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return false
			}
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // Loopback HTTP cannot use Secure; other safeguards are set.
				Name:     playgroundSessionCookie,
				Value:    sessionToken,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			target := *r.URL
			values := target.Query()
			values.Del("token")
			target.RawQuery = values.Encode()
			if target.Path == "" {
				target.Path = "/web"
			}
			http.Redirect( //nolint:gosec // target is derived only from this loopback request with its token removed.
				w,
				r,
				target.String(),
				http.StatusSeeOther,
			)
			return false
		}
	}
	cookie, err := r.Cookie(playgroundSessionCookie)
	if err != nil || cookie.Value != sessionToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func newPlaygroundSessionToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("create playground session token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

func proxyOpenEnvToSandbox(
	w http.ResponseWriter,
	r *http.Request,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
	runtimeSessions ...*project.WebSocketRuntimeSession,
) {
	operation := strings.Trim(r.URL.Path, "/")
	switch operation {
	case "health", "state", "metadata", "schema":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	case "reset", "step":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if len(runtimeSessions) > 0 && runtimeSessions[0] != nil &&
		(operation == "reset" || operation == "step" || operation == "state") {
		proxyStatefulOpenEnvOperation(w, r, operation, runtimeSessions[0])
		return
	}

	targetUrl, err := project.RuntimeOperationURL(sandboxUrl, operation)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// sandboxUrl is the active RLE sandbox URL; operation is restricted above.
	target, err := http.NewRequestWithContext(r.Context(), r.Method, targetUrl, r.Body) //nolint:gosec
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target.Header.Set("Accept", "application/json")
	if authorizationProvider != nil {
		authorization, err := authorizationProvider(r.Context())
		if err != nil {
			http.Error(w, "failed to authenticate to environment runtime", http.StatusBadGateway)
			return
		}
		target.Header.Set("Authorization", authorization)
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		target.Header.Set("Content-Type", contentType)
	}
	// The local UI proxy forwards only fixed OpenEnv operations to the active sandbox.
	resp, err := project.HTTPClient(60).Do(target) //nolint:gosec
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func proxyStatefulOpenEnvOperation(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	runtimeSession *project.WebSocketRuntimeSession,
) {
	payload := ""
	if operation == "reset" || operation == "step" {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 100*1024*1024))
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		payload = string(body)
	}
	if operation == "step" {
		var request struct {
			Action json.RawMessage `json:"action"`
		}
		if err := json.Unmarshal([]byte(payload), &request); err != nil || len(request.Action) == 0 {
			http.Error(w, "step requires an action", http.StatusBadRequest)
			return
		}
		payload = string(request.Action)
	}
	response, err := runtimeSession.CallAndDrain(r.Context(), operation, payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, response) //nolint:gosec // The response is served as JSON, not executable HTML.
}
