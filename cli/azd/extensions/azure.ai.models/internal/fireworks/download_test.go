// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fireworks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetDownloadEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != "/v1/accounts/test-account/models/test-model:getDownloadEndpoint" ||
			r.Header.Get("Authorization") != "Bearer provider-test-key" {
			t.Errorf("unexpected export request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"filenameToSignedUrls":{"config.json":"https://storage.example/file?sig=source-secret"}}`)
	}))
	defer server.Close()
	c := &Client{endpoint: server.URL, apiKey: "provider-test-key", httpClient: server.Client()}
	files, err := c.GetDownloadEndpoints(t.Context(), "test-account", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if files["config.json"] != "https://storage.example/file?sig=source-secret" {
		t.Fatal("signed URL was not preserved for the transfer")
	}
}

func TestDownloadModelExportFailuresDoNotCreateDirectory(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"missing inventory", 200, `{}`, "no downloadable files"},
		{"empty inventory", 200, `{"filenameToSignedUrls":{}}`, "no downloadable files"},
		{"API key restricted", 403, `{"message":"API keys cannot download models"}`, "API keys cannot download models"},
		{"account restricted", 400,
			`{"message":"model downloading is restricted, please contact Fireworks AI to get access"}`,
			"model downloading is restricted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			c := &Client{endpoint: server.URL, apiKey: "provider-test-key", httpClient: server.Client()}
			directory := filepath.Join(t.TempDir(), "model")
			_, err := c.DownloadModel(t.Context(), "test-account", "test-model", directory)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed export created a directory: %v", err)
			}
		})
	}
}

func TestDownloadFilesStreamsAllExportsWithoutCredentials(t *testing.T) {
	//nolint:gosec // These are synthetic model file fixtures, not credentials.
	content := map[string]string{
		"/config.json":           `{"model_type":"custom"}`,
		"/nested/tokenizer.json": `{"version":"1"}`,
		"/model.bin":             "model file bytes",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("storage request must be an unauthenticated GET")
		}
		if r.URL.Query().Get("sig") != "source-secret" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("signed URL or byte-preserving transfer headers were not preserved")
		}
		body, ok := content[r.URL.Path]
		if !ok {
			t.Errorf("unexpected file request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	files := map[string]string{}
	var expectedBytes int64
	for name, body := range content {
		files[strings.TrimPrefix(name, "/")] = server.URL + name + "?sig=source-secret"
		expectedBytes += int64(len(body))
	}
	directory := filepath.Join(t.TempDir(), "model")
	result, err := downloadFiles(t.Context(), files, directory, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != len(content) || result.Bytes != expectedBytes || result.Directory != directory {
		t.Fatalf("unexpected download summary: %+v", result)
	}
	for name, expected := range content {
		//nolint:gosec // Paths come from the static fixture map and remain under t.TempDir().
		actual, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(strings.TrimPrefix(name, "/"))))
		if err != nil || string(actual) != expected {
			t.Fatalf("file %q = %q, error = %v", name, actual, err)
		}
	}
}

func TestDownloadFilesValidatesEntireInventoryBeforeWriting(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"empty", nil},
		{"dot", map[string]string{".": "https://storage.example/file"}},
		{"traversal", map[string]string{
			"good.json": "https://storage.example/file", "../escape": "https://storage.example/file",
		}},
		{"absolute path", map[string]string{"/model": "https://storage.example/file"}},
		{"backslash traversal", map[string]string{`a\..\escape`: "https://storage.example/file"}},
		{"drive path", map[string]string{"C:/model": "https://storage.example/file"}},
		{"file directory conflict", map[string]string{
			"config": "https://storage.example/file", "config/model": "https://storage.example/file",
		}},
		{"non HTTPS", map[string]string{"config.json": "http://storage.example/file?sig=source-secret"}},
		//nolint:gosec // Deliberately invalid credentials exercise URL rejection.
		{"embedded credentials", map[string]string{
			"config.json": "https://user:password@storage.example/file?sig=source-secret#fragment",
		}},
		{"fragment", map[string]string{"config.json": "https://storage.example/file?sig=source-secret#fragment"}},
		{"malformed URL", map[string]string{"config.json": "https://%invalid/file?sig=source-secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "model")
			httpClient := &http.Client{Transport: downloadTransport(func(*http.Request) (*http.Response, error) {
				t.Error("validation failure must not make any storage requests")
				return nil, errors.New("unexpected request")
			})}
			_, err := downloadFiles(t.Context(), tt.files, directory, httpClient)
			if err == nil {
				t.Fatal("accepted an invalid export inventory")
			}
			for _, secret := range []string{"password", "source-secret", "sig=", "fragment"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("validation error disclosed %q", secret)
				}
			}
			if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid inventory created a directory: %v", err)
			}
		})
	}
}

func TestDownloadFilesProtectsExistingDestination(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "config.json")
	if err := os.WriteFile(existing, []byte("existing content"), 0o600); err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: downloadTransport(func(*http.Request) (*http.Response, error) {
		t.Error("existing destination must be rejected before any transfer")
		return nil, errors.New("unexpected request")
	})}
	_, err := downloadFiles(t.Context(), map[string]string{"config.json": "https://storage.example/file"},
		directory, httpClient)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error = %v, want existing directory error", err)
	}
	//nolint:gosec // The existing fixture is a fixed filename under t.TempDir().
	actual, err := os.ReadFile(existing)
	if err != nil || string(actual) != "existing content" {
		t.Fatalf("existing file changed: %q, %v", actual, err)
	}
}

func TestDownloadFileDoesNotReplaceExistingFile(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("model.bin", []byte("existing content"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "new content")
	}))
	defer server.Close()
	_, err = downloadFile(t.Context(), server.Client(), root, "model.bin", server.URL+"/file")
	if err == nil || !strings.Contains(err.Error(), "conflicts with an existing file") {
		t.Fatalf("unexpected error: %v", err)
	}
	actual, err := root.ReadFile("model.bin")
	if err != nil || string(actual) != "existing content" {
		t.Fatalf("existing file changed: %q, %v", actual, err)
	}
}

func TestDownloadFilesRejectsRedirectsAndRedactsStorageErrors(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/file" {
					t.Error("followed a signed download redirect")
				}
				w.Header().Set("Location", "/redirect?sig=redirect-secret")
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, "private storage error: sig=source-secret")
			}))
			defer server.Close()
			directory := filepath.Join(t.TempDir(), "model")
			_, err := downloadFiles(t.Context(), map[string]string{"model.bin": server.URL + "/file?sig=source-secret"},
				directory, server.Client())
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, secret := range []string{"source-secret", "redirect-secret", "private storage error", server.URL} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("download error disclosed %q", secret)
				}
			}
		})
	}
}

func TestDownloadFilesRemovesPartialFileAndKeepsCompletedFiles(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/complete" {
			_, _ = fmt.Fprint(w, "complete")
			return
		}
		w.Header().Set("Content-Length", "100")
		_, _ = fmt.Fprint(w, "truncated")
	}))
	defer server.Close()
	directory := filepath.Join(t.TempDir(), "model")
	_, err := downloadFiles(t.Context(), map[string]string{
		"a-complete.json": server.URL + "/complete",
		"b-failed.bin":    server.URL + "/truncated?sig=source-secret",
	}, directory, server.Client())
	if err == nil || !strings.Contains(err.Error(), "1 completed file(s) remain") {
		t.Fatalf("unexpected transfer error: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "a-complete.json" {
		t.Fatalf("partial files were not cleaned up: %v, %v", entries, err)
	}
}

func TestDownloadFilesCancellationAndTransportErrors(t *testing.T) {
	t.Run("transport error", func(t *testing.T) {
		httpClient := &http.Client{Transport: downloadTransport(func(r *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: errors.New("private transport diagnostics")}
		})}
		_, err := downloadFiles(t.Context(),
			map[string]string{"model.bin": "https://storage.example/file?sig=source-secret"},
			filepath.Join(t.TempDir(), "model"), httpClient)
		if err == nil {
			t.Fatal("transport failure was ignored")
		}
		for _, secret := range []string{"source-secret", "sig=", "storage.example", "private transport diagnostics"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("transport error disclosed %q", secret)
			}
		}
	})
	t.Run("canceled transfer", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		httpClient := &http.Client{Transport: downloadTransport(func(*http.Request) (*http.Response, error) {
			body := downloadReader(func([]byte) (int, error) {
				cancel()
				return 0, context.Canceled
			})
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body)}, nil
		})}
		directory := filepath.Join(t.TempDir(), "model")
		_, err := downloadFiles(ctx, map[string]string{"model.bin": "https://storage.example/file"},
			directory, httpClient)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want cancellation", err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatalf("cancellation left incomplete files: %v, %v", entries, err)
		}
	})
}

type downloadTransport func(*http.Request) (*http.Response, error)

func (f downloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type downloadReader func([]byte) (int, error)

func (f downloadReader) Read(p []byte) (int, error) { return f(p) }
