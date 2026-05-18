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

	"azure.ai.training/pkg/models"
)

// MaxAttempts is the number of attempts for retryable API calls (initial + retries).
const MaxAttempts = 3

// DefaultParallelism is the default number of concurrent downloads when the
// caller doesn't specify one.
const DefaultParallelism = 8

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
		parallelism = DefaultParallelism
	}

	results := make([]ArtifactDownloadResult, len(infos))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	// Dedupe by destination path. The history service should not normally
	// return duplicate artifact paths, but if it does, two goroutines would
	// race on the same .tmp file and final rename — risking corrupt output.
	// Keep the first occurrence; mark subsequent duplicates as skipped.
	seen := make(map[string]struct{}, len(infos))

	for i, info := range infos {
		if info == nil || info.ContentURI == "" || info.Path == "" {
			results[i] = ArtifactDownloadResult{Path: "", Err: fmt.Errorf("missing content uri or path")}
			continue
		}
		if _, dup := seen[info.Path]; dup {
			results[i] = ArtifactDownloadResult{
				Path: info.Path,
				Err:  fmt.Errorf("duplicate artifact path %q in response; skipped", info.Path),
			}
			continue
		}
		seen[info.Path] = struct{}{}
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
	outPath, err := safeJoin(destDir, relPath)
	if err != nil {
		return 0, err
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
			// Don't echo the response body in user-facing errors: Azure Blob
			// Storage XML error responses can include request URLs, signature
			// fragments, or other server-echoed content that may leak SAS
			// tokens / credentials. Status code alone is enough for triage.
			return resp.StatusCode, fmt.Errorf("download %s: HTTP %d", relPath, resp.StatusCode)
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
		// Always remove the tmp file unless we successfully rename it. This
		// covers ctx cancellation mid-Copy, transport errors, and any other
		// soft-failure path. (Hard process kill on Windows ctrl+C bypasses
		// defers; SweepTempFiles handles that case on the next run.)
		renamed := false
		defer func() {
			if !renamed {
				_ = os.Remove(tmpPath)
			}
		}()
		n, copyErr := io.Copy(f, resp.Body)
		closeErr := f.Close()
		if copyErr != nil {
			return 0, copyErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
		if err := os.Rename(tmpPath, outPath); err != nil {
			return 0, err
		}
		renamed = true
		written = n
		return resp.StatusCode, nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// SweepTempFiles removes any leftover *.tmp files under root. We use these as
// scratch files for atomic download writes; they should never survive a
// successful run. They can linger if the process is hard-killed (e.g. ctrl+C
// on Windows bypasses Go defers), so we sweep them at the start of each run
// to keep the destination tree clean.
func SweepTempFiles(root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort; ignore unreadable dirs
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tmp") {
			_ = os.Remove(path)
		}
		return nil
	})
}

// safeJoin joins relPath onto destDir while rejecting any path that resolves
// outside destDir (e.g., "../../etc/foo" or, on Windows, "C:\foo"). This
// guards against path-traversal / Zip-Slip writes when relPath comes from an
// untrusted source such as an API response.
func safeJoin(destDir, relPath string) (string, error) {
	outPath := filepath.Join(destDir, filepath.FromSlash(relPath))
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("resolve dest dir: %w", err)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path: %w", err)
	}
	if absOut != absDest && !strings.HasPrefix(absOut, absDest+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes destination directory", relPath)
	}
	return outPath, nil
}
