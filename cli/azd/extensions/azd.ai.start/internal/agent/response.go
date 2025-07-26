// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"azd.ai.start/internal/session"
	"azd.ai.start/internal/validation"
)

// AgentResponse represents the complete response from the agent
type AgentResponse struct {
	Output     string
	Session    *session.ActionSession
	Validation *validation.ValidationResult
}

// NewAgentResponse creates a new agent response
func NewAgentResponse(output string, sess *session.ActionSession, validationResult *validation.ValidationResult) *AgentResponse {
	return &AgentResponse{
		Output:     output,
		Session:    sess,
		Validation: validationResult,
	}
}
