// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package download

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		want       bool
	}{
		// --- non-retryable: cancellation / deadline ---
		{
			name: "context canceled is not retryable",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "context deadline exceeded is not retryable",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "context canceled wrapped in url.Error is still not retryable",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: context.Canceled},
			want: false,
		},

		// --- retryable: transient HTTP statuses ---
		{
			name:       "HTTP 429 is retryable",
			statusCode: http.StatusTooManyRequests,
			want:       true,
		},
		{
			name:       "HTTP 500 is retryable",
			statusCode: http.StatusInternalServerError,
			want:       true,
		},
		{
			name:       "HTTP 503 is retryable",
			statusCode: http.StatusServiceUnavailable,
			want:       true,
		},
		{
			name:       "HTTP 599 is retryable (boundary)",
			statusCode: 599,
			want:       true,
		},

		// --- non-retryable: client/auth/redirect statuses ---
		{
			name:       "HTTP 200 is not retryable",
			statusCode: http.StatusOK,
			want:       false,
		},
		{
			name:       "HTTP 400 is not retryable",
			statusCode: http.StatusBadRequest,
			want:       false,
		},
		{
			name:       "HTTP 401 is not retryable",
			statusCode: http.StatusUnauthorized,
			want:       false,
		},
		{
			name:       "HTTP 403 is not retryable",
			statusCode: http.StatusForbidden,
			want:       false,
		},
		{
			name:       "HTTP 404 is not retryable",
			statusCode: http.StatusNotFound,
			want:       false,
		},

		// --- retryable: transport errors ---
		{
			name: "url.Error (transport failure) is retryable",
			err:  &url.Error{Op: "Get", URL: "https://x", Err: errors.New("dial tcp: i/o timeout")},
			want: true,
		},

		// --- non-retryable: arbitrary non-transport errors ---
		{
			name: "plain non-transport error is not retryable",
			err:  errors.New("parse failure"),
			want: false,
		},

		// --- nothing went wrong ---
		{
			name: "nil err and zero status is not retryable",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryable(tt.err, tt.statusCode)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSafeJoin(t *testing.T) {
	dest := t.TempDir()
	sep := string(filepath.Separator)

	tests := []struct {
		name    string
		relPath string
		wantErr bool
	}{
		{
			name:    "simple relative path is allowed",
			relPath: "outputs/model.bin",
		},
		{
			name:    "nested path is allowed",
			relPath: "a/b/c/d.txt",
		},
		{
			name:    "single file is allowed",
			relPath: "log.txt",
		},
		{
			name:    "parent traversal is rejected",
			relPath: "../escape.txt",
			wantErr: true,
		},
		{
			name:    "deep parent traversal is rejected",
			relPath: "../../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "embedded parent traversal is rejected",
			relPath: "outputs/../../escape.txt",
			wantErr: true,
		},
		{
			name:    "current-dir prefix is allowed (resolves inside)",
			relPath: "./outputs/model.bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeJoin(dest, tt.relPath)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "escapes destination directory")
				return
			}
			require.NoError(t, err)
			// Resolved path must live under dest.
			absDest, _ := filepath.Abs(dest)
			absGot, _ := filepath.Abs(got)
			assert.True(t,
				strings.HasPrefix(absGot, absDest+sep) || absGot == absDest,
				"expected %q to be under %q", absGot, absDest,
			)
		})
	}
}

// TestSafeJoin_OSAbsolutePath documents that an OS-absolute path supplied as
// relPath is *not* treated as an escape: filepath.Join strips the leading
// separator / drive letter on both POSIX and Windows, so the result is safely
// re-rooted under destDir. We assert the resolved path stays inside dest.
func TestSafeJoin_OSAbsolutePath(t *testing.T) {
	dest := t.TempDir()
	other := t.TempDir() // a different absolute path
	abs, err := filepath.Abs(other)
	require.NoError(t, err)

	got, err := safeJoin(dest, abs)
	require.NoError(t, err)
	absDest, _ := filepath.Abs(dest)
	absGot, _ := filepath.Abs(got)
	assert.True(t,
		strings.HasPrefix(absGot, absDest+string(filepath.Separator)) || absGot == absDest,
		"expected %q to be re-rooted under %q", absGot, absDest,
	)
}
