// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/azdignore"
)

// TestFetchAzdIgnoreMatcher_NotInTree returns nil without making any
// HTTP request when the tree listing does not include .azdignore. This
// is the common case and must not pay a network round-trip.
func TestFetchAzdIgnoreMatcher_NotInTree(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tree := []treeEntry{
		{Type: "blob", Path: "infra/main.bicep"},
		{Type: "blob", Path: "azure.yaml"},
	}

	got := fetchAzdIgnoreMatcher(t.Context(), srv.Client(), "owner/repo", "main", "", tree)
	if got != nil {
		t.Errorf("expected nil matcher when tree has no %s", azdignore.FileName)
	}
	if called {
		t.Error("expected no HTTP call when tree has no .azdignore")
	}
}

// TestFetchAzdIgnoreMatcher_RewriteToTestServer uses a custom RoundTripper
// to redirect the raw.githubusercontent.com URL to a local test server
// that serves a known .azdignore body, then verifies the returned
// matcher correctly identifies ignored paths.
func TestFetchAzdIgnoreMatcher_RewriteToTestServer(t *testing.T) {
	t.Parallel()

	body := "*.log\ninfra/secrets/\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+azdignore.FileName) {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	httpClient := &http.Client{Transport: redirectTo(t, srv.URL)}
	tree := []treeEntry{
		{Type: "blob", Path: azdignore.FileName},
		{Type: "blob", Path: "azure.yaml"},
		{Type: "blob", Path: "infra/main.bicep"},
		{Type: "blob", Path: "infra/secrets/password.txt"},
		{Type: "blob", Path: "infra/debug.log"},
	}

	matcher := fetchAzdIgnoreMatcher(t.Context(), httpClient, "owner/repo", "main", "", tree)
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	tests := []struct {
		path string
		want bool
	}{
		{"azure.yaml", false},
		{"infra/main.bicep", false},
		{"infra/secrets/password.txt", true},
		{"infra/debug.log", true},
	}
	for _, tc := range tests {
		ignored := azdignore.IsIgnored(matcher, tc.path)
		if ignored != tc.want {
			t.Errorf("path %s: ignored=%v, want %v", tc.path, ignored, tc.want)
		}
	}
}

// TestFetchAzdIgnoreMatcher_FetchErrorReturnsNil verifies that a server
// error during the raw fetch is swallowed: the from-code flow must fall
// back to the unfiltered behavior rather than failing.
func TestFetchAzdIgnoreMatcher_FetchErrorReturnsNil(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := &http.Client{Transport: redirectTo(t, srv.URL)}
	tree := []treeEntry{{Type: "blob", Path: azdignore.FileName}}

	got := fetchAzdIgnoreMatcher(t.Context(), httpClient, "owner/repo", "main", "", tree)
	if got != nil {
		t.Errorf("expected nil matcher on non-200 response")
	}
}

// redirectTo returns a RoundTripper that rewrites every outbound
// request to target the given base URL. Used to redirect the
// hard-coded raw.githubusercontent.com URL to a test server.
func redirectTo(t *testing.T, baseURL string) http.RoundTripper {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", baseURL, err)
	}
	return &rewriteTransport{base: u}
}

type rewriteTransport struct {
	base *url.URL
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	newURL := *rt.base
	newURL.Path = req.URL.Path
	r.URL = &newURL
	r.Host = newURL.Host
	return http.DefaultTransport.RoundTrip(r)
}
