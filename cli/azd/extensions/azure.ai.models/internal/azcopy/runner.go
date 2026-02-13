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
// Priority: explicit path > PATH > well-known locations.
func NewRunner(explicitPath string) (*Runner, error) {
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

	return nil, fmt.Errorf("azcopy not found. Install it from https://learn.microsoft.com/azure/storage/common/storage-use-azcopy-v10\n" +
		"Or specify the path with --azcopy-path")
}

// Path returns the resolved azcopy binary path.
func (r *Runner) Path() string {
	return r.azcopyPath
}

// Copy runs azcopy copy from source to sasURI with real-time progress display.
func (r *Runner) Copy(ctx context.Context, source string, sasURI string) error {
	// If directory, append /* for contents
	sourceArg := source
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("source path not found: %s", source)
	}
	if info.IsDir() {
		sourceArg = filepath.Join(source, "*")
	}

	args := []string{
		"copy",
		sourceArg,
		sasURI,
		"--recursive=true",
		"--output-type", "json",
		"--block-size-mb", "100",
	}

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

	filled := int(percent / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
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

	transferredStr := formatBytes(transferred)
	totalStr := formatBytes(total)

	fmt.Fprintf(os.Stdout, "\r  %s %.1f%% (%s / %s) | %s | Elapsed: %s | ETA: %s   ",
		bar, percent, transferredStr, totalStr, speedStr, formatDuration(elapsed), etaStr)
}

func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
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
