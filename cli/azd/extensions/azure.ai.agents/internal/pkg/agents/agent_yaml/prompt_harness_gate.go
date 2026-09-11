// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// NewPromptHarness returns a harness block naming only its type, which is the
// whole of the block for an agent that takes the harness defaults. It returns
// nil for an empty type so callers can pass an unresolved harness straight
// through and get a plain prompt agent.
func NewPromptHarness(harnessType string) *PromptHarness {
	harnessType = strings.TrimSpace(harnessType)
	if harnessType == "" {
		return nil
	}
	return &PromptHarness{Type: harnessType}
}

// HarnessType returns the harness discriminator the agent runs on, or "" for a
// plain prompt agent with no harness.
func (p PromptAgent) HarnessType() string {
	if p.Harness == nil {
		return ""
	}
	return strings.TrimSpace(p.Harness.Type)
}

// ValidateHarnessBlock rejects a malformed `harness:` block.
//
// Each rule mirrors one the service enforces, so failing here turns an opaque
// API rejection into a message that names the offending key.
func (p PromptAgent) ValidateHarnessBlock() error {
	if p.Harness == nil {
		return nil
	}
	if p.HarnessType() == "" {
		return fmt.Errorf(
			"agent.yaml declares a harness with no type; set harness.type (for example %q), "+
				"or remove the harness block to run as a plain prompt agent",
			agent_api.ManagedAgentHarnessGitHubCopilot,
		)
	}
	return nil
}
