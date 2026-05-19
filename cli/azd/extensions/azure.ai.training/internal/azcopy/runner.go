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

// endOfJobContent is the parsed inner JSON from MessageContent when
// MessageType is "EndOfJob". This is azcopy's authoritative status record —
// when a job fails partway, JobStatus and FailedTransfers are the only
// place the actual reason surfaces (the process exit status is just 1).
type endOfJobContent struct {
	JobID              string           `json:"JobID"`
	JobStatus          string           `json:"JobStatus"`
	TotalTransfers     int              `json:"TotalTransfers"`
	TransfersCompleted int              `json:"TransfersCompleted"`
	TransfersFailed    int              `json:"TransfersFailed"`
	TransfersSkipped   int              `json:"TransfersSkipped"`
	FailedTransfers    []failedTransfer `json:"FailedTransfers"`
	ErrorMsg           string           `json:"ErrorMsg"`
}

// failedTransfer is a single entry in EndOfJob.FailedTransfers.
type failedTransfer struct {
	Src               string `json:"Src"`
	Dst               string `json:"Dst"`
	TransferStatus    string `json:"TransferStatus"`
	TransferStatusStr string `json:"TransferStatusStr"`
	ErrorCode         int    `json:"ErrorCode"`
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

	// Capture azcopy stderr to a bounded buffer rather than piping it live to
	// our own stderr. The progress bar uses "\r" repaints with no newline, so a
	// stderr line that arrives mid-transfer would be silently overwritten by
	// the next repaint. Buffering lets us dump the full text on any failure
	// path, which is often the only signal we get when azcopy exits before
	// emitting an EndOfJob record (e.g. SAS/auth failure during enumeration).
	stderrBuf := &cappedBuffer{max: 16 * 1024}
	cmd.Stderr = stderrBuf

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

	var (
		lastBytesOverWire    int64
		endOfJob             endOfJobContent
		gotEndOfJob          bool
		warnedUnparseable    bool
		warnedProgressFormat bool
		initMessage          string   // first "Init" content — contains log file path
		infoMessages         []string // collected "Info" content — often the only failure clue
	)
	const maxInfoMessages = 20
	for scanner.Scan() {
		line := scanner.Text()

		var msg azcopyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// azcopy's NDJSON schema is stable, but if it ever drifts we want a
			// breadcrumb instead of silent "0 bytes" progress. Log only the first
			// occurrence to avoid spamming.
			if !warnedUnparseable {
				warnedUnparseable = true
				fmt.Fprintf(os.Stderr, "\n  azcopy: unrecognized output line (%v): %s\n", err, truncate(line, 200))
			}
			continue
		}

		switch msg.MessageType {
		case "Progress":
			var progress progressContent
			if err := json.Unmarshal([]byte(msg.MessageContent), &progress); err != nil {
				if !warnedProgressFormat {
					warnedProgressFormat = true
					fmt.Fprintf(os.Stderr, "\n  azcopy: unrecognized Progress payload (%v)\n", err)
				}
				continue
			}
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
		case "Error":
			fmt.Fprintf(os.Stderr, "\n  azcopy error: %s\n", msg.MessageContent)
		case "Init":
			// First Init message contains the on-disk log file path; remember
			// it so we can surface it on failure even when no other diagnostic
			// channel produces output.
			if initMessage == "" {
				initMessage = msg.MessageContent
			}
		case "Info":
			// azcopy frequently routes failure context ("cannot list container...",
			// auth errors, etc.) through Info messages when --output-type=json.
			// Buffer the most recent few so we can dump them on failure.
			if len(infoMessages) < maxInfoMessages {
				infoMessages = append(infoMessages, msg.MessageContent)
			} else {
				// Keep the most recent maxInfoMessages by dropping the oldest.
				infoMessages = append(infoMessages[1:], msg.MessageContent)
			}
		case "EndOfJob":
			fmt.Fprintln(os.Stdout)
			// Capture the payload; we surface details below only on failure
			// so a successful run doesn't spam the user with diagnostic noise.
			if err := json.Unmarshal([]byte(msg.MessageContent), &endOfJob); err == nil {
				gotEndOfJob = true
			}
		}
	}

	// Capture any scanner error (e.g. truncated pipe, read failure) before waiting
	// on the process. We always call cmd.Wait so the child is reaped and its exit
	// status is authoritative, but if the scanner failed we surface that too —
	// otherwise a mid-stream pipe error could hide a real azcopy failure.
	scanErr := scanner.Err()

	if err := cmd.Wait(); err != nil {
		elapsed := time.Since(startTime)
		fmt.Fprintf(os.Stdout, "\n  azcopy failed after %s\n", formatDuration(elapsed))
		fmt.Fprintf(os.Stderr, "  azcopy source: %s\n", redactSAS(sourceArg))
		printEndOfJobDiagnostics(gotEndOfJob, endOfJob)
		printCapturedStderr(stderrBuf)
		printAzcopyInfo(initMessage, infoMessages)
		if scanErr != nil {
			return fmt.Errorf("azcopy failed: %w (also: error reading azcopy output: %s)", err, scanErr.Error())
		}
		return fmt.Errorf("azcopy failed: %w", err)
	}

	if scanErr != nil {
		elapsed := time.Since(startTime)
		fmt.Fprintf(os.Stdout, "\n  azcopy status uncertain after %s\n", formatDuration(elapsed))
		fmt.Fprintf(os.Stderr, "  azcopy source: %s\n", redactSAS(sourceArg))
		printEndOfJobDiagnostics(gotEndOfJob, endOfJob)
		printCapturedStderr(stderrBuf)
		printAzcopyInfo(initMessage, infoMessages)
		return fmt.Errorf("azcopy exited successfully but its output stream failed mid-transfer: %w", scanErr)
	}

	// azcopy can exit 0 even when the job's JobStatus is "CompletedWithErrors"
	// or "Failed" — surface that explicitly so failures aren't silently swallowed.
	if gotEndOfJob && !isCompletedStatus(endOfJob.JobStatus) {
		elapsed := time.Since(startTime)
		fmt.Fprintf(os.Stdout, "\n  azcopy reported %s after %s\n", endOfJob.JobStatus, formatDuration(elapsed))
		fmt.Fprintf(os.Stderr, "  azcopy source: %s\n", redactSAS(sourceArg))
		printEndOfJobDiagnostics(gotEndOfJob, endOfJob)
		printCapturedStderr(stderrBuf)
		printAzcopyInfo(initMessage, infoMessages)
		return fmt.Errorf("azcopy job ended with status %q (%d failed, %d skipped of %d transfers)",
			endOfJob.JobStatus, endOfJob.TransfersFailed, endOfJob.TransfersSkipped, endOfJob.TotalTransfers)
	}

	elapsed := time.Since(startTime)
	fmt.Fprintf(os.Stdout, "  Completed in %s\n", formatDuration(elapsed))

	return nil
}

// isCompletedStatus reports whether the JobStatus value from azcopy's EndOfJob
// payload represents a fully successful job. azcopy uses "Completed" for full
// success; "CompletedWithErrors", "CompletedWithSkipped", "Failed", "Cancelled"
// all indicate something the caller should know about.
func isCompletedStatus(status string) bool {
	// Empty status (couldn't parse EndOfJob) is treated as "don't know" — caller
	// already handles the cmd.Wait() error path so we only reach this on a
	// 0-exit + missing/empty EndOfJob, which we let pass.
	return status == "" || status == "Completed"
}

// printEndOfJobDiagnostics writes a compact, human-readable summary of azcopy's
// EndOfJob payload to stderr. Called on any failure path so the user sees the
// actual reason ("404 on blob X") instead of a bare exit-status-1 message.
func printEndOfJobDiagnostics(got bool, eoj endOfJobContent) {
	if !got {
		fmt.Fprintln(os.Stderr, "  azcopy: no EndOfJob diagnostics were emitted")
		return
	}
	fmt.Fprintf(os.Stderr, "  azcopy job status: %s (transfers: %d total, %d completed, %d failed, %d skipped)\n",
		eoj.JobStatus, eoj.TotalTransfers, eoj.TransfersCompleted, eoj.TransfersFailed, eoj.TransfersSkipped)
	if eoj.ErrorMsg != "" {
		fmt.Fprintf(os.Stderr, "  azcopy error: %s\n", eoj.ErrorMsg)
	}
	const maxShown = 5
	for i, ft := range eoj.FailedTransfers {
		if i >= maxShown {
			fmt.Fprintf(os.Stderr, "  ... and %d more failed transfer(s)\n", len(eoj.FailedTransfers)-maxShown)
			break
		}
		status := ft.TransferStatusStr
		if status == "" {
			status = ft.TransferStatus
		}
		fmt.Fprintf(os.Stderr, "    - %s [status=%s, code=%d]\n", ft.Src, status, ft.ErrorCode)
	}
}

// truncate returns s shortened to at most n runes, appending an ellipsis
// when truncation occurs. Used for safe inclusion of raw azcopy output in
// diagnostic messages without flooding stderr.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// cappedBuffer is an io.Writer that retains the first `max` bytes written to
// it and silently discards the rest. Used to capture azcopy's stderr without
// risk of unbounded memory growth on a runaway child process; max is sized
// for human-readable error messages, not bulk data.
type cappedBuffer struct {
	max     int
	buf     []byte
	dropped int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.max - len(c.buf)
	if remaining > 0 {
		take := len(p)
		if take > remaining {
			take = remaining
		}
		c.buf = append(c.buf, p[:take]...)
		c.dropped += len(p) - take
	} else {
		c.dropped += len(p)
	}
	// Always report all bytes consumed so the child process doesn't block on
	// a full pipe; the dropped count is preserved for diagnostics.
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	return string(c.buf)
}

// printCapturedStderr writes any buffered azcopy stderr to our own stderr,
// prefixed for readability. Called from failure paths so the user sees the
// real cause even when azcopy died before emitting an EndOfJob record.
func printCapturedStderr(buf *cappedBuffer) {
	if buf == nil {
		return
	}
	s := strings.TrimRight(buf.String(), "\r\n\t ")
	if s == "" {
		return
	}
	fmt.Fprintln(os.Stderr, "  azcopy stderr:")
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(os.Stderr, "    %s\n", strings.TrimRight(line, "\r"))
	}
	if buf.dropped > 0 {
		fmt.Fprintf(os.Stderr, "    ... (%d more bytes truncated)\n", buf.dropped)
	}
}

// printAzcopyInfo writes the captured Init message (which carries the on-disk
// log file path) and any buffered Info messages to stderr. With
// --output-type=json azcopy often routes the real failure context through
// these channels rather than through "Error" or stderr, so on failure we need
// to surface them — otherwise the user is left with bare "exit status 1".
func printAzcopyInfo(initMessage string, infoMessages []string) {
	if initMessage != "" {
		fmt.Fprintln(os.Stderr, "  azcopy init:")
		for _, line := range strings.Split(initMessage, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "    %s\n", line)
		}
	}
	if len(infoMessages) > 0 {
		fmt.Fprintln(os.Stderr, "  azcopy info (most recent):")
		for _, m := range infoMessages {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "    %s\n", truncate(m, 500))
		}
	}
}

// redactSAS returns a URL with its query string replaced by "?[sas-redacted]"
// so SAS tokens and other credentials are never written to logs/stderr. The
// path portion — which is what we usually need to diagnose scope problems —
// is preserved verbatim.
func redactSAS(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + "?[sas-redacted]"
	}
	return u
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
