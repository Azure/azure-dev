// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcopy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"azure.ai.training/internal/utils"
)

// Runner manages azcopy execution.
type Runner struct {
	azcopyPath string
}

// azcopyMessage is the top-level JSON message from azcopy --output-type json.
type azcopyMessage struct {
	TimeStamp      string `json:"TimeStamp"`
	MessageType    string `json:"MessageType"`
	MessageContent string `json:"MessageContent"`
}

// progressContent is the parsed inner JSON from MessageContent when MessageType is "Progress".
// All numeric values are strings in azcopy's JSON output.
type progressContent struct {
	TotalBytesTransferred string `json:"TotalBytesTransferred"`
	TotalBytesEnumerated  string `json:"TotalBytesEnumerated"`
	TotalBytesExpected    string `json:"TotalBytesExpected"`
	BytesOverWire         string `json:"BytesOverWire"`
	PercentComplete       string `json:"PercentComplete"`
	TransfersCompleted    string `json:"TransfersCompleted"`
	TotalTransfers        string `json:"TotalTransfers"`
	JobStatus             string `json:"JobStatus"`
}

// NewRunner creates a new azcopy runner, discovering the azcopy binary.
// Priority: explicit path > PATH > well-known locations > auto-download.
func NewRunner(ctx context.Context, explicitPath string) (*Runner, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return nil, fmt.Errorf("azcopy not found at specified path: %s", explicitPath)
		}
		return &Runner{azcopyPath: explicitPath}, nil
	}

	// Search PATH
	if path, err := exec.LookPath("azcopy"); err == nil {
		return &Runner{azcopyPath: path}, nil
	}

	// Check well-known locations
	wellKnown := getWellKnownPaths()
	for _, p := range wellKnown {
		if _, err := os.Stat(p); err == nil {
			return &Runner{azcopyPath: p}, nil
		}
	}

	// Auto-download as last resort
	fmt.Println("  azcopy not found. Downloading latest version...")
	path, err := downloadAzCopy(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to auto-install azcopy: %w\n"+
			"Install manually from https://learn.microsoft.com/azure/storage/common/storage-use-azcopy-v10\n"+
			"Or specify the path with --azcopy-path", err)
	}
	fmt.Printf("  azcopy installed to %s\n\n", path)
	return &Runner{azcopyPath: path}, nil
}

// Path returns the resolved azcopy binary path.
func (r *Runner) Path() string {
	return r.azcopyPath
}

// Copy runs azcopy copy from source to sasURI with real-time progress display.
// Source can be a local file/directory path or a remote URL (e.g., blob URL with SAS token).
//
// Use this when the source is a single blob, a single local file, or when you
// want azcopy's default folder-preserving behavior. For remote folders/containers
// where you want the *contents* extracted directly into the destination (no
// container-name directory wrapping), use CopyContents instead.
func (r *Runner) Copy(ctx context.Context, source string, sasURI string) error {
	return r.copy(ctx, source, sasURI, false /*forceContents*/)
}

// CopyContents runs azcopy copy from a remote folder/container source to sasURI,
// rewriting the source URL so azcopy copies the *contents* rather than the
// folder itself. Use this for blob containers (URLs of shape
// https://account.blob.core.windows.net/<container>?<sas>) so the destination
// doesn't end up nested under a container-name directory.
//
// For local sources or single-blob URLs, use Copy instead.
func (r *Runner) CopyContents(ctx context.Context, source string, sasURI string) error {
	return r.copy(ctx, source, sasURI, true /*forceContents*/)
}

// copy is the shared implementation behind Copy and CopyContents.
func (r *Runner) copy(ctx context.Context, source, sasURI string, forceContents bool) error {
	sourceArg := source

	// Only do local path handling for non-URL sources
	if !isRemoteURL(source) {
		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("source path not found: %s", source)
		}
		if info.IsDir() {
			sourceArg = filepath.Join(source, "*")
		}
	} else if forceContents {
		// Caller explicitly asked for contents-of-folder semantics: ensure the
		// path has a wildcard so azcopy doesn't preserve the container name as
		// a directory under the destination.
		sourceArg = forceWildcardRemoteSource(source)
	} else {
		// For remote URLs ending with "/" (directory-like), append "*" before the query
		// string so azcopy copies the contents rather than the folder itself.
		// Skip if user already included a wildcard.
		sourceArg = normalizeRemoteSourceURL(source)
	}

	args := []string{
		"copy",
		sourceArg,
		sasURI,
		"--recursive=true",
		"--output-type", "json",
		"--block-size-mb", "100",
	}

	//nolint:gosec // azcopyPath is resolved from known install directory
	cmd := exec.CommandContext(ctx, r.azcopyPath, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	startTime := time.Now()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start azcopy: %w", err)
	}

	// Parse NDJSON lines from stdout
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastBytesOverWire int64
	for scanner.Scan() {
		line := scanner.Text()

		var msg azcopyMessage
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}

		switch msg.MessageType {
		case "Progress":
			var progress progressContent
			if json.Unmarshal([]byte(msg.MessageContent), &progress) == nil {
				bytesOverWire, _ := strconv.ParseInt(progress.BytesOverWire, 10, 64)
				totalExpected, _ := strconv.ParseInt(progress.TotalBytesExpected, 10, 64)
				percent, _ := strconv.ParseFloat(progress.PercentComplete, 64)

				// Use BytesOverWire for smoother progress since TotalBytesTransferred
				// only updates when entire blocks complete
				if totalExpected > 0 && bytesOverWire > 0 {
					wirePercent := float64(bytesOverWire) / float64(totalExpected) * 100
					if wirePercent > percent {
						percent = wirePercent
					}
				}

				// Cap at 100% — BytesOverWire includes protocol overhead
				if percent > 100 {
					percent = 100
				}
				displayBytes := bytesOverWire
				if totalExpected > 0 && displayBytes > totalExpected {
					displayBytes = totalExpected
				}

				if bytesOverWire > lastBytesOverWire {
					lastBytesOverWire = bytesOverWire
					printProgress(displayBytes, totalExpected, percent, startTime)
				}
			}
		case "Error":
			fmt.Fprintf(os.Stderr, "\n  azcopy error: %s\n", msg.MessageContent)
		case "EndOfJob":
			fmt.Fprintln(os.Stdout)
		}
	}

	if err := cmd.Wait(); err != nil {
		elapsed := time.Since(startTime)
		fmt.Fprintf(os.Stdout, "\n  Upload failed after %s\n", formatDuration(elapsed))
		return fmt.Errorf("azcopy failed: %w", err)
	}

	elapsed := time.Since(startTime)
	fmt.Fprintf(os.Stdout, "  Completed in %s\n", formatDuration(elapsed))

	return nil
}

func printProgress(transferred, total int64, percent float64, startTime time.Time) {
	const barWidth = 35

	filled := min(int(percent/100*float64(barWidth)), barWidth)
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)

	elapsed := time.Since(startTime)

	// Calculate speed (MB/s)
	var speedStr string
	elapsedSec := elapsed.Seconds()
	if elapsedSec > 0 && transferred > 0 {
		speedMBps := float64(transferred) / (1024 * 1024) / elapsedSec
		speedStr = fmt.Sprintf("%.1f MB/s", speedMBps)
	} else {
		speedStr = "-- MB/s"
	}

	// Calculate ETA
	var etaStr string
	if percent > 0 && percent < 100 {
		totalEstimated := elapsed.Seconds() / (percent / 100)
		remaining := time.Duration((totalEstimated - elapsed.Seconds()) * float64(time.Second))
		etaStr = formatDuration(remaining)
	} else if percent >= 100 {
		etaStr = "done"
	} else {
		etaStr = "calculating..."
	}

	transferredStr := utils.FormatBytes(transferred)
	totalStr := utils.FormatBytes(total)

	fmt.Fprintf(os.Stdout, "\r  %s %.1f%% (%s / %s) | %s | Elapsed: %s | ETA: %s   ",
		bar, percent, transferredStr, totalStr, speedStr, formatDuration(elapsed), etaStr)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %02dm", h, m)
}

func getWellKnownPaths() []string {
	home, _ := os.UserHomeDir()
	binary := "azcopy"
	if runtime.GOOS == "windows" {
		binary = "azcopy.exe"
	}

	paths := []string{
		filepath.Join(home, ".azd", "bin", binary),
		filepath.Join(home, ".azure", "bin", binary),
	}

	// Check default package manager location on Linux
	if runtime.GOOS == "linux" {
		paths = append(paths, "/usr/bin/azcopy")
	}

	// Check common download locations on Windows
	if runtime.GOOS == "windows" {
		downloads := filepath.Join(home, "Downloads")
		entries, err := os.ReadDir(downloads)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "azcopy_windows") {
					candidate := filepath.Join(downloads, entry.Name(), binary)
					paths = append(paths, candidate)
				}
			}
		}
	}

	return paths
}

// normalizeRemoteSourceURL ensures a remote source URL copies contents rather than the folder.
// If the URL path ends with "/" and doesn't already have a wildcard, inserts "*" before the query string.
func normalizeRemoteSourceURL(source string) string {
	// If user already provided a wildcard, use as-is
	if strings.Contains(source, "/*") {
		return source
	}

	// Split on "?" to separate path from query string
	parts := strings.SplitN(source, "?", 2)
	path := parts[0]

	if strings.HasSuffix(path, "/") {
		path += "*"
	}

	if len(parts) == 2 {
		return path + "?" + parts[1]
	}
	return path
}

// forceWildcardRemoteSource rewrites a remote source URL to ensure azcopy
// copies the *contents* of the referenced container/folder rather than
// preserving the folder/container name as a directory under the destination.
//
// Behavior:
//   - If the URL already contains a wildcard ("/*"), return as-is.
//   - Otherwise, ensure the path ends with "/*" before any query string.
//
// Example:
//
//	in:  https://acct.blob.core.windows.net/container?sig=abc
//	out: https://acct.blob.core.windows.net/container/*?sig=abc
func forceWildcardRemoteSource(source string) string {
	if strings.Contains(source, "/*") {
		return source
	}

	parts := strings.SplitN(source, "?", 2)
	path := strings.TrimSuffix(parts[0], "/") + "/*"

	if len(parts) == 2 {
		return path + "?" + parts[1]
	}
	return path
}

// isRemoteURL checks if the source string is a remote URL (http/https).
func isRemoteURL(source string) bool {
	return strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://")
}
