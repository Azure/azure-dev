// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rollouts

import (
	"context"
	"time"
)

// Entry summarizes one rollout a training job recorded, without its graph.
//
// A training run produces thousands of rollouts, so the list view reads these
// summaries and fetches the full response only for the rollout being opened.
type Entry struct {
	RolloutID    string    `json:"rollout_id"`
	Sequence     int       `json:"sequence"`
	Split        string    `json:"split"`
	Reward       *float64  `json:"reward,omitempty"`
	Success      *bool     `json:"success,omitempty"`
	LatencySec   float64   `json:"latency_s,omitempty"`
	HasGraph     bool      `json:"has_graph"`
	SavedAt      time.Time `json:"saved_at,omitzero"`
	Step         *int      `json:"step,omitempty"`
	CheckpointID string    `json:"checkpoint_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
}

// Lister enumerates recorded rollouts so the dashboard can offer a choice.
//
// A Reader alone cannot open a training job, because the rollout IDs a run
// generates are not known to the caller beforehand.
//
// List returns the rollouts recorded after the given rollout ID, or the whole
// run when it is empty. A running job keeps appending rollouts, so the
// dashboard re-lists from where it stopped rather than re-reading the run.
type Lister interface {
	List(ctx context.Context, after string) ([]Entry, error)
}
