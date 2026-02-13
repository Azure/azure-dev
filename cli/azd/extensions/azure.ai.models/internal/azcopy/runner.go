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
	"strings"
)

// Runner manages azcopy execution.
type Runner struct {
	azcopyPath string
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

// Copy runs azcopy copy from source to sasURI, streaming progress to the terminal.
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
	}

	cmd := exec.CommandContext(ctx, r.azcopyPath, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start azcopy: %w", err)
	}

	// Parse JSON lines from stdout for progress
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastPercent float64
	for scanner.Scan() {
		line := scanner.Text()

		var msg struct {
			MessageType     string  `json:"MessageType"`
			MessageContent  string  `json:"MessageContent"`
			PercentComplete float64 `json:"PercentComplete"`
		}

		if json.Unmarshal([]byte(line), &msg) == nil {
			switch msg.MessageType {
			case "Progress":
				if msg.PercentComplete > lastPercent {
					lastPercent = msg.PercentComplete
					printProgress(msg.PercentComplete)
				}
			case "Error":
				fmt.Fprintf(os.Stderr, "\nazcopy error: %s\n", msg.MessageContent)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("azcopy failed: %w", err)
	}

	return nil
}

func printProgress(percent float64) {
	const barWidth = 40
	filled := int(percent / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)
	fmt.Fprintf(os.Stdout, "\r  %s %.1f%%", bar, percent)
	if percent >= 100 {
		fmt.Fprintln(os.Stdout)
	}
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
