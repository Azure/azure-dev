// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package session

import (
	"time"
)

// ActionLog represents a single action taken by the agent
type ActionLog struct {
	Timestamp time.Time
	Action    string
	Tool      string
	Input     string
	Output    string
	Success   bool
	Duration  time.Duration
}

// NewActionLog creates a new action log
func NewActionLog(tool, input string) *ActionLog {
	return &ActionLog{
		Timestamp: time.Now(),
		Tool:      tool,
		Action:    tool,
		Input:     input,
	}
}

// SetOutput sets the output and success status for the action
func (al *ActionLog) SetOutput(output string, success bool) {
	al.Output = output
	al.Success = success
	al.Duration = time.Since(al.Timestamp)
}

// SetDuration sets the duration for the action
func (al *ActionLog) SetDuration(duration time.Duration) {
	al.Duration = duration
}
