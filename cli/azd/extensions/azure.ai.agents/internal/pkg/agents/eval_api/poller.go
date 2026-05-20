// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// JobStatus — typed status with terminal/failed semantics
// ---------------------------------------------------------------------------

// JobStatus represents the normalized status of a generation job.
type JobStatus string

const (
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
	JobStatusCanceled  JobStatus = "canceled"
)

// ParseJobStatus normalizes a raw status string into a JobStatus.
// An empty string is treated as "running".
func ParseJobStatus(s string) JobStatus {
	if s == "" {
		return JobStatusRunning
	}
	return JobStatus(strings.ToLower(s))
}

// IsTerminal returns true when the status represents a final state.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case JobStatusCompleted, JobStatusSucceeded, JobStatusFailed, JobStatusCancelled, JobStatusCanceled:
		return true
	}
	return false
}

// IsFailed returns true when the status represents a failure or cancellation.
func (s JobStatus) IsFailed() bool {
	switch s {
	case JobStatusFailed, JobStatusCancelled, JobStatusCanceled:
		return true
	}
	return false
}

// String returns the status as a plain string.
func (s JobStatus) String() string {
	return string(s)
}

// ---------------------------------------------------------------------------
// JobFailedError — returned when a polled job reaches a failed state
// ---------------------------------------------------------------------------

// JobFailedError is returned when a generation job reaches a failed terminal state.
type JobFailedError struct {
	Job    *GenerationJob
	Status JobStatus
}

func (e *JobFailedError) Error() string {
	if e.Job != nil && e.Job.Error != nil && e.Job.Error.Message != "" {
		return fmt.Sprintf("job failed with status %q: %s", e.Status, e.Job.Error.Message)
	}
	return fmt.Sprintf("job failed with status %q", e.Status)
}

// ---------------------------------------------------------------------------
// PollerTimeoutError — returned when polling exhausts all attempts
// ---------------------------------------------------------------------------

// PollerTimeoutError is returned when a generation job has not reached a
// terminal state within the configured number of polling attempts.
type PollerTimeoutError struct {
	OperationID string
	Attempts    int
}

func (e *PollerTimeoutError) Error() string {
	return fmt.Sprintf(
		"operation %s did not complete within %d attempts",
		e.OperationID, e.Attempts,
	)
}

// ---------------------------------------------------------------------------
// GetJobFunc — callback type for fetching job state
// ---------------------------------------------------------------------------

// GetJobFunc fetches the current state of a generation job by operation ID.
type GetJobFunc func(ctx context.Context, operationID, apiVersion string) (*GenerationJob, error)

// ---------------------------------------------------------------------------
// PollerOptions — configurable polling behavior
// ---------------------------------------------------------------------------

// PollerOptions configures the polling interval and attempt limit.
type PollerOptions struct {
	Interval    time.Duration
	MaxAttempts int
}

// DefaultPollerOptions returns sensible defaults: 2 s interval, 300 attempts (~10 min).
func DefaultPollerOptions() PollerOptions {
	return PollerOptions{
		Interval:    2 * time.Second,
		MaxAttempts: 300,
	}
}

// ---------------------------------------------------------------------------
// Poller — polls a generation job until it reaches a terminal state
// ---------------------------------------------------------------------------

// Poller polls a GenerationJob until it reaches a terminal status.
type Poller struct {
	OperationID string
	APIVersion  string
	GetJob      GetJobFunc
	Options     PollerOptions
	// OnPoll is called after each successful poll with the latest status.
	// Callers can use this for progress reporting (e.g. debug logging).
	OnPoll func(status JobStatus)
}

// NewPoller creates a Poller with default options.
func NewPoller(operationID, apiVersion string, getJob GetJobFunc) *Poller {
	return &Poller{
		OperationID: operationID,
		APIVersion:  apiVersion,
		GetJob:      getJob,
		Options:     DefaultPollerOptions(),
	}
}

// Poll blocks until the job reaches a terminal state, the context is
// cancelled, or the maximum number of attempts is exhausted.
//
// On success it returns the completed GenerationJob.
// On failure it returns a *JobFailedError (which wraps the job for inspection).
// On timeout it returns a plain error.
func (p *Poller) Poll(ctx context.Context) (*GenerationJob, error) {
	if p.OperationID == "" {
		return nil, fmt.Errorf("operation ID is empty")
	}

	for range p.Options.MaxAttempts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.Options.Interval):
		}

		job, err := p.GetJob(ctx, p.OperationID, p.APIVersion)
		if err != nil {
			return nil, err
		}

		status := ParseJobStatus(job.Status)
		log.Printf("[poller] operationID=%s status=%s", p.OperationID, status)

		if p.OnPoll != nil {
			p.OnPoll(status)
		}

		if status.IsTerminal() {
			if status.IsFailed() {
				return nil, &JobFailedError{Job: job, Status: status}
			}
			return job, nil
		}
	}

	return nil, &PollerTimeoutError{
		OperationID: p.OperationID,
		Attempts:    p.Options.MaxAttempts,
	}
}
