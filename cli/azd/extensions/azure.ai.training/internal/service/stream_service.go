// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"azure.ai.training/internal/utils"
	"azure.ai.training/pkg/client"
)

// StreamService handles polling and streaming log output from a running job.
type StreamService struct {
	client *client.Client
}

// NewStreamService creates a new stream service.
func NewStreamService(apiClient *client.Client) *StreamService {
	return &StreamService{client: apiClient}
}

// Log file patterns matching the Azure ML SDK behavior.
// Primary: Common Runtime user logs.
// Fallback: Legacy azureml-logs for older compute targets.
var (
	commonRuntimeLogPattern = regexp.MustCompile(`user_logs/std_log[\D]*[0]*(?:_ps)?\.txt`)
	legacyLogPattern        = regexp.MustCompile(`azureml-logs/[\d]{2}.+\.txt`)
)

// terminalStates are job statuses that indicate the job has finished.
var terminalStates = map[string]bool{
	"Completed":     true,
	"Failed":        true,
	"Canceled":      true,
	"NotResponding": true,
	"Paused":        true,
}

// activeStates are job statuses where streaming is applicable.
var activeStates = map[string]bool{
	"NotStarted":   true,
	"Queued":       true,
	"Preparing":    true,
	"Provisioning": true,
	"Starting":     true,
	"Running":      true,
	"Finalizing":   true,
}

// StreamResult contains the final state of a streamed job.
type StreamResult struct {
	JobName   string
	Status    string
	StudioURL string
}

// StreamJobLogs polls the job and streams log output until the job reaches a terminal state.
func (s *StreamService) StreamJobLogs(ctx context.Context, jobName string) (*StreamResult, error) {
	fmt.Fprintf(os.Stderr, "Streaming logs for job: %s\n\n", jobName)

	// Line-count tracking per file, matching the Azure ML SDK approach.
	// Each poll downloads full content, skips already-printed lines, prints the rest.
	processedLines := make(map[string]int)

	const maxConsecutiveErrs = 3

	startTime := time.Now()
	consecutiveErrs := 0
	trackingEndpoint := ""
	firstPoll := true
	multiFile := false // latches to true once >1 file is seen

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Check job status every poll
		job, err := s.client.GetJob(ctx, jobName)
		if err != nil {
			consecutiveErrs++
			if consecutiveErrs >= maxConsecutiveErrs {
				return nil, fmt.Errorf("failed to get job status after %d retries: %w", maxConsecutiveErrs, err)
			}
			if err := sleepWithContext(ctx, pollInterval(startTime)); err != nil {
				return nil, err
			}
			continue
		}
		consecutiveErrs = 0

		jobStatus := job.Properties.Status
		studioURL := utils.ServiceEndpoint(job.Properties.Services, "Studio")

		if trackingEndpoint == "" {
			trackingEndpoint = utils.ServiceEndpoint(job.Properties.Services, "Tracking")
		}

		if terminalStates[jobStatus] {
			if trackingEndpoint != "" && !firstPoll {
				s.flushLogs(ctx, trackingEndpoint, jobName, processedLines, multiFile)
			}
			return &StreamResult{
				JobName:   jobName,
				Status:    jobStatus,
				StudioURL: studioURL,
			}, nil
		}

		if !activeStates[jobStatus] {
			fmt.Fprintf(os.Stderr, "Job status: %s, waiting...\n", jobStatus)
			if err := sleepWithContext(ctx, pollInterval(startTime)); err != nil {
				return nil, err
			}
			firstPoll = false
			continue
		}

		// Stream logs if tracking endpoint is available
		if trackingEndpoint != "" {
			var newMultiFile bool
			_, newMultiFile, err = s.pollAndPrintLogs(ctx, trackingEndpoint, jobName, processedLines, multiFile)
			if newMultiFile {
				multiFile = true
			}
			if err != nil {
				consecutiveErrs++
				if consecutiveErrs >= maxConsecutiveErrs {
					return nil, fmt.Errorf("failed to stream logs after %d retries: %w", maxConsecutiveErrs, err)
				}
			} else {
				consecutiveErrs = 0
			}
		} else {
			fmt.Fprintf(os.Stderr, "Waiting for job to initialize...\n")
		}

		firstPoll = false
		if err := sleepWithContext(ctx, pollInterval(startTime)); err != nil {
			return nil, err
		}
	}
}

// sleepWithContext sleeps for the given duration, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// pollInterval returns the polling interval using a sigmoid curve from 2s → 60s
// based on elapsed time since streaming started. Matches the Azure ML SDK approach.
//
//	duration = MAX / (1.0 + 100 * exp(-elapsed_seconds / 20.0))
//	return max(MIN, duration)
func pollInterval(startTime time.Time) time.Duration {
	const (
		minInterval = 2 * time.Second
		maxInterval = 60 * time.Second
	)
	elapsed := time.Since(startTime).Seconds()
	durationSec := 60.0 / (1.0 + 100.0*math.Exp(-elapsed/20.0))
	duration := time.Duration(durationSec * float64(time.Second))
	if duration < minInterval {
		return minInterval
	}
	return duration
}

// filterLogFiles selects streamable log files from the history details.
// Matches Common Runtime user logs first; falls back to legacy azureml-logs.
// Returns matched file names sorted alphabetically.
func filterLogFiles(logFiles map[string]string) []string {
	var matched []string
	for name := range logFiles {
		if commonRuntimeLogPattern.MatchString(name) {
			matched = append(matched, name)
		}
	}
	if len(matched) == 0 {
		// Fallback to legacy log pattern for older compute targets
		for name := range logFiles {
			if legacyLogPattern.MatchString(name) {
				matched = append(matched, name)
			}
		}
	}
	sort.Strings(matched)
	return matched
}

// printFileHeader prints a visual separator for a log file, matching the Azure ML SDK style.
func printFileHeader(fileName string) {
	fmt.Println()
	fmt.Printf("Streaming %s\n", fileName)
	fmt.Println(strings.Repeat("=", len("Streaming ")+len(fileName)))
	fmt.Println()
}

// pollAndPrintLogs fetches run history details and prints only new log lines.
// Returns whether new content was printed and whether multiple files were seen.
func (s *StreamService) pollAndPrintLogs(
	ctx context.Context,
	trackingEndpoint string,
	jobName string,
	processedLines map[string]int,
	multiFile bool,
) (bool, bool, error) {
	details, err := s.client.GetRunHistoryDetails(ctx, trackingEndpoint, jobName)
	if err != nil {
		return false, multiFile, err
	}
	if details == nil || len(details.LogFiles) == 0 {
		return false, multiFile, nil
	}

	fileNames := filterLogFiles(details.LogFiles)
	if len(fileNames) == 0 {
		return false, multiFile, nil
	}

	// Latch multiFile once we see >1 file — formatting stays consistent thereafter
	if len(fileNames) > 1 {
		multiFile = true
	}

	hasNewContent := false
	for _, fileName := range fileNames {
		sasURI := details.LogFiles[fileName]

		content, _, err := s.client.GetBlobContent(ctx, sasURI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read log file %s: %v\n", fileName, err)
			continue
		}
		if content == "" {
			continue
		}

		lines := strings.Split(content, "\n")
		// Remove trailing empty element from final newline
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		previousLines := processedLines[fileName]
		if len(lines) <= previousLines {
			continue
		}

		// Print file header: always on first encounter, or on every switch for multi-file jobs
		if previousLines == 0 || multiFile {
			printFileHeader(fileName)
		}

		for _, line := range lines[previousLines:] {
			fmt.Println(line)
		}
		hasNewContent = true
		processedLines[fileName] = len(lines)
	}

	return hasNewContent, multiFile, nil
}

// flushLogs does a final poll to capture any remaining log output.
func (s *StreamService) flushLogs(
	ctx context.Context,
	trackingEndpoint string,
	jobName string,
	processedLines map[string]int,
	multiFile bool,
) {
	_, _, err := s.pollAndPrintLogs(ctx, trackingEndpoint, jobName, processedLines, multiFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to flush final logs: %v\n", err)
	}
}
