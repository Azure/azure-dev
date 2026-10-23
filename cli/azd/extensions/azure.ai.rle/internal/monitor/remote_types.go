// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"azure.ai.rle/internal/rollouts"
)

// JobRollout is the lightweight, project-scoped RLE rollout metadata.
type JobRollout struct {
	RolloutID       string     `json:"rollout_id"`
	JobID           string     `json:"job_id"`
	Sequence        *int64     `json:"sequence_id,omitempty"`
	EnvironmentName string     `json:"environment_name"`
	EnvironmentVer  string     `json:"environment_version"`
	CreatedAt       time.Time  `json:"created_at_utc,omitzero"`
	Status          string     `json:"status"`
	Reward          *float64   `json:"reward,omitempty"`
	Success         *bool      `json:"success,omitempty"`
	Latency         *float64   `json:"latency_s,omitempty"`
	CheckpointID    string     `json:"checkpoint_id,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	SavedAt         *time.Time `json:"saved_at,omitempty"`
	HasGraph        bool       `json:"has_graph"`
	ResultAvailable bool       `json:"result_available"`
}

// JobPage holds a service page and its opaque continuation token.
type JobPage[T any] struct {
	Data []T    `json:"data"`
	Next string `json:"nextContinuationToken"`
}

// JobSource reads real-service job data without local artifact files.
type JobSource interface {
	Config(context.Context) (json.RawMessage, error)
	Metrics(context.Context, int64, string) (JobPage[json.RawMessage], error)
	Rollouts(context.Context, string) (JobPage[JobRollout], error)
	Detail(context.Context, string) (JobRollout, error)
	Events(context.Context, string, string) (JobPage[json.RawMessage], error)
	Result(context.Context, JobRollout) (rollouts.Snapshot, error)
	Status(context.Context) (string, error)
}

// ReadError preserves service status, throttling, and correlation IDs without
// exposing response bodies.
type ReadError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
	// Operation and Request identify the failed call for service investigation.
	Operation string
	Request   string
}

func (e *ReadError) Error() string {
	message := fmt.Sprintf("monitor service returned HTTP %d (%s)", e.Status, e.Code)
	if e.Operation != "" {
		message += "; operation ID " + e.Operation
	}
	if e.Request != "" {
		message += "; request ID " + e.Request
	}
	return message
}
