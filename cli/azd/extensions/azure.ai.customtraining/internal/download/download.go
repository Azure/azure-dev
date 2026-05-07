// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package download provides helpers for downloading job outputs and artifacts.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"azure.ai.customtraining/pkg/models"
)

// MaxAttempts is the number of attempts for retryable API calls (initial + retries).
const MaxAttempts = 3

// initialBackoff is the delay before the first retry; subsequent retries double it.
const initialBackoff = 500 * time.Millisecond

// retryHTTPClient is a dedicated HTTP client used for direct contentUri downloads.
// No bearer token is needed for those URLs (the SAS in the query string authorizes the read).
var retryHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// IsRetryable reports whether an error or response status warrants another attempt.
// Only transient failures retry: transport errors (DNS/connection/timeout),
// HTTP 429, and 5xx. Auth failures, 4xx, and context cancellation/deadline
// are non-retryable so we don't waste time or ignore user cancel.
func IsRetryable(err error, statusCode int) bool {
	// Never retry on context cancellation/deadline.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Retry transient HTTP statuses.
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode >= 500 && statusCode <= 599 {
		return true
	}
	// Retry transport-layer errors (surfaced as *url.Error by net/http).
	if err != nil {
		var urlErr *url.Error
		return errors.As(err, &urlErr)
	}
	return false
}

// WithRetry calls fn up to MaxAttempts times, sleeping with exponential backoff between attempts.
// fn should return (statusCode, err). If statusCode is 0 the result is judged solely by err.
// Returns the last error from fn after all attempts are exhausted.
func WithRetry(ctx context.Context, fn func() (int, error)) error {
	backoff := initialBackoff
	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		status, err := fn()
		if err == nil && status < 400 {
			return nil
		}
		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
		if attempt == MaxAttempts || !IsRetryable(err, status) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}

// ArtifactDownloadResult tracks per-artifact outcome for summary reporting.
type ArtifactDownloadResult struct {
	Path  string
	Bytes int64
	Err   error
}

// DownloadArtifacts downloads a list of artifacts in parallel into destDir,
// preserving each artifact's relative path (from contentinfo.Path).
// parallelism controls the number of concurrent downloads.
func DownloadArtifacts(
	ctx context.Context,
	infos []*models.RunArtifactContentInfo,
	destDir string,
	parallelism int,
) []ArtifactDownloadResult {
	if parallelism <= 0 {
		parallelism = 8
	}

	results := make([]ArtifactDownloadResult, len(infos))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	for i, info := range infos {
		i, info := i, info
		if info == nil || info.ContentURI == "" || info.Path == "" {
			results[i] = ArtifactDownloadResult{Path: "", Err: fmt.Errorf("missing content uri or path")}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			n, err := downloadOne(ctx, info.ContentURI, destDir, info.Path)
			results[i] = ArtifactDownloadResult{Path: info.Path, Bytes: n, Err: err}
		}()
	}
	wg.Wait()
	return results
}

// downloadOne performs a retryable GET on contentURI and writes the body to destDir/relPath.
// Parent directories are created as needed.
func downloadOne(ctx context.Context, contentURI, destDir, relPath string) (int64, error) {
	outPath := filepath.Join(destDir, filepath.FromSlash(relPath))
	// Defense-in-depth: relPath comes from the API response. Reject any path
	// that resolves outside destDir (e.g., "../../etc/foo" or an absolute path
	// on Windows like "C:\foo") to prevent path-traversal / Zip-Slip writes.
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return 0, fmt.Errorf("resolve dest dir: %w", err)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return 0, fmt.Errorf("resolve artifact path: %w", err)
	}
	if absOut != absDest && !strings.HasPrefix(absOut, absDest+string(filepath.Separator)) {
		return 0, fmt.Errorf("artifact path %q escapes destination directory", relPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, fmt.Errorf("create dir: %w", err)
	}

	var written int64
	err = WithRetry(ctx, func() (int, error) {
		written = 0
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, contentURI, nil)
		if err != nil {
			return 0, err
		}
		resp, err := retryHTTPClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return resp.StatusCode, fmt.Errorf("download %s: HTTP %d: %s",
				relPath, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		// Write to a sibling .tmp file and atomically rename on success so a
		// partial/interrupted download (network drop, ctx canceled, disk full)
		// never leaves a truncated file at outPath that callers might mistake
		// for a successful download. os.Rename is atomic on the same filesystem
		// across Linux, macOS, and Windows; tmp + final live in the same dir.
		tmpPath := outPath + ".tmp"
		f, err := os.Create(tmpPath)
		if err != nil {
			return 0, err
		}
		n, copyErr := io.Copy(f, resp.Body)
		closeErr := f.Close()
		if copyErr != nil {
			os.Remove(tmpPath)
			return 0, copyErr
		}
		if closeErr != nil {
			os.Remove(tmpPath)
			return 0, closeErr
		}
		if err := os.Rename(tmpPath, outPath); err != nil {
			os.Remove(tmpPath)
			return 0, err
		}
		written = n
		return resp.StatusCode, nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}
