// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
)

// harnessBuiltInCapabilities are the built-in capability groups a harnessed
// agent may allow or exclude, in the order they are reported.
//
// Unlike tool types, this list *is* closed: `builtin_tools` filters a fixed set
// the harness provides, so a name outside it can only be a typo, and silently
// dropping it would leave the author believing they had turned a capability off.
var harnessBuiltInCapabilities = []string{
	"filesystem_read",
	"filesystem_write",
	"shell",
	"subagents",
	"web",
}

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
	if p.HarnessType() != agent_api.ManagedAgentHarnessGitHubCopilot {
		return nil
	}
	if err := p.validateHarnessEnvironment(); err != nil {
		return err
	}
	return p.validateHarnessBuiltInTools()
}

// validateHarnessEnvironment rejects a half-specified sandbox size.
//
// cpu and memory are a pair: the service refuses one without the other rather
// than defaulting the missing half, so a manifest setting only `cpu` would fail
// at deploy with no indication that `memory` is what is missing.
func (p PromptAgent) validateHarnessEnvironment() error {
	env := p.Harness.Environment
	if env == nil {
		return nil
	}
	cpu := strings.TrimSpace(env.Cpu)
	memory := strings.TrimSpace(env.Memory)
	if (cpu == "") == (memory == "") {
		return nil
	}

	set, missing := "cpu", "memory"
	if cpu == "" {
		set, missing = "memory", "cpu"
	}
	return fmt.Errorf(
		"agent.yaml sets harness.environment.%s without harness.environment.%s; "+
			"the %q harness sizes the sandbox from both, so set them together or set neither",
		set, missing, p.HarnessType(),
	)
}

// validateHarnessBuiltInTools rejects capability names the harness does not
// define, in either the allowed or the excluded list.
func (p PromptAgent) validateHarnessBuiltInTools() error {
	builtin := p.Harness.BuiltinTools
	if builtin == nil {
		return nil
	}

	var unknown []string
	check := func(field string, names *[]string) {
		if names == nil {
			return
		}
		for _, name := range *names {
			name = strings.TrimSpace(name)
			if slices.Contains(harnessBuiltInCapabilities, name) {
				continue
			}
			entry := fmt.Sprintf("%s.%s", field, name)
			if !slices.Contains(unknown, entry) {
				unknown = append(unknown, entry)
			}
		}
	}
	check("allowed", builtin.Allowed)
	check("excluded", builtin.Excluded)

	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)

	return fmt.Errorf(
		"agent.yaml lists harness.builtin_tools.%s, which the %q harness does not define; "+
			"supported capabilities are %s",
		strings.Join(unknown, ", harness.builtin_tools."),
		p.HarnessType(),
		strings.Join(harnessBuiltInCapabilities, ", "),
	)
}
