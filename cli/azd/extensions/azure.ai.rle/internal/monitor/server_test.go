// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/rollouts"
	"azure.ai.rle/internal/ui"
)

const testHost = "127.0.0.1:12345"
const testToken = "test-session-code"

func TestHandlerAuthorizationAndRoutes(t *testing.T) {
	snapshot := rollouts.Snapshot{
		Response: json.RawMessage(`{"rollout_id":"abc","reward":1,"result":"<script>alert(1)</script>"}`),
		Source:   "Saved local result",
	}
	handler, err := newHandler(snapshot, testHost, testToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, host, origin, authorization, cookie string
		status                                                  int
	}{
		{name: "public shell", method: "GET", path: "/", status: 200},
		{name: "no cookie", method: "GET", path: "/api/rollout", status: 401},
		{name: "bad cookie", method: "GET", path: "/api/rollout", cookie: "wrong", status: 401},
		{name: "authorized", method: "GET", path: "/api/rollout", cookie: testToken, status: 200},
		{name: "foreign host", method: "GET", path: "/api/rollout", host: "evil.test", cookie: testToken, status: 403},
		{name: "foreign origin", method: "GET", path: "/api/rollout", origin: "https://evil.test",
			cookie: testToken, status: 403},
		{name: "sign in", method: "POST", path: "/session", authorization: "Bearer " + testToken, status: 204},
		{name: "sign in rejects foreign origin", method: "POST", path: "/session",
			authorization: "Bearer " + testToken, origin: "http://evil.test", status: 403},
		{name: "wrong code", method: "POST", path: "/session", authorization: "Bearer wrong", status: 401},
		{name: "read only", method: "POST", path: "/api/rollout", cookie: testToken, status: 405},
		{name: "unknown route", method: "GET", path: "/api/other", cookie: testToken, status: 404},
		{name: "no directories", method: "GET", path: "/web/", status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://"+testHost+tc.path, nil)
			if tc.host != "" {
				request.Host = tc.host
			}
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("Authorization", tc.authorization)
			if tc.cookie != "" {
				request.AddCookie(&http.Cookie{
					Name: sessionCookie + "-" + strings.ReplaceAll(testHost, ":", "-"), Value: tc.cookie,
				})
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" ||
				recorder.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing privacy/security headers")
			}
			if tc.status == 204 {
				cookies := recorder.Result().Cookies()
				if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
					t.Fatalf("unexpected session cookie: %v", cookies)
				}
			}
			if tc.name == "authorized" {
				var got rollouts.Snapshot
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(got.Response), "reward") {
					t.Fatal("missing rollout response")
				}
			} else if strings.Contains(recorder.Body.String(), "alert(1)") {
				t.Fatal("result leaked outside authorized data route")
			}
		})
	}
}

func TestModuleAssetsUseJavaScriptMIMEType(t *testing.T) {
	handler, err := newHandler(rollouts.Snapshot{Response: json.RawMessage(`{}`)}, testHost, testToken)
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
				t.Fatal("printed URL contains credentials")
			}
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(link)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("monitor not responsive: %d", response.StatusCode)
			}
			if !noBrowser {
				select {
				case link := <-opened:
					if !strings.Contains(link, "#token=") {
						t.Fatal("automatic browser link lacks bootstrap token")
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
