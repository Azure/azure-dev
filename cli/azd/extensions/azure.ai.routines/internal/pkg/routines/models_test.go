// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTriggerCLIToWire_AllEntriesPresent(t *testing.T) {
	expected := map[string]string{
		"recurring":    "schedule",
		"timer":        "timer",
		"github-issue": "github_issue",
	}
	assert.Equal(t, expected, TriggerCLIToWire,
		"TriggerCLIToWire must contain all documented CLI→wire mappings")
}

func TestActionCLIToWire_AllEntriesPresent(t *testing.T) {
	expected := map[string]string{
		"agent-response": "invoke_agent_responses_api",
		"agent-invoke":   "invoke_agent_invocations_api",
	}
	assert.Equal(t, expected, ActionCLIToWire,
		"ActionCLIToWire must contain all documented CLI→wire mappings")
}

func TestDefaultKeys(t *testing.T) {
	assert.Equal(t, "default", DefaultTriggerKey)
	assert.Equal(t, "default", DefaultActionKey)
}

func TestTriggerCLIToWire_NoUnknownEntries(t *testing.T) {
	// Ensure no extra/typo entries sneak in.
	for k := range TriggerCLIToWire {
		switch k {
		case "recurring", "timer", "github-issue":
			// OK
		default:
			t.Errorf("unexpected key %q in TriggerCLIToWire", k)
		}
	}
}

func TestActionCLIToWire_NoUnknownEntries(t *testing.T) {
	for k := range ActionCLIToWire {
		switch k {
		case "agent-response", "agent-invoke":
			// OK
		default:
			t.Errorf("unexpected key %q in ActionCLIToWire", k)
		}
	}
}
