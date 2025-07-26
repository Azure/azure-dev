// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package session

import (
	"time"
)

// ActionSession tracks the current conversation session and actions
type ActionSession struct {
	InitialIntent    string
	PlannedActions   []string
	ExecutedActions  []ActionLog
	ValidationResult interface{} // Use interface{} to avoid circular dependency
	StartTime        time.Time
	EndTime          time.Time
}

// NewActionSession creates a new action session
func NewActionSession(initialIntent string) *ActionSession {
	return &ActionSession{
		InitialIntent:   initialIntent,
		PlannedActions:  []string{},
		ExecutedActions: []ActionLog{},
		StartTime:       time.Now(),
	}
}

// Start marks the session as started
func (as *ActionSession) Start() {
	as.StartTime = time.Now()
}

// End marks the session as ended
func (as *ActionSession) End() {
	as.EndTime = time.Now()
}

// AddExecutedAction adds an executed action to the session
func (as *ActionSession) AddExecutedAction(action ActionLog) {
	as.ExecutedActions = append(as.ExecutedActions, action)
}

// SetValidationResult sets the validation result for the session
func (as *ActionSession) SetValidationResult(result interface{}) {
	as.ValidationResult = result
}
