// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatConsentDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      string
		action     string
		operation  string
		target     string
		permission string
		expect     []string
		notExpect  []string
	}{
		{
			name:       "all_fields",
			scope:      "project",
			action:     "read",
			operation:  "list",
			target:     "env",
			permission: "write",
			expect: []string{
				"Scope: project",
				"Action: read",
				"Context: list",
				"Target: env",
				"Permission: write",
			},
		},
		{
			name:      "partial_fields",
			scope:     "global",
			action:    "deploy",
			operation: "",
			target:    "",
			permission: "",
			expect:    []string{"Scope: global", "Action: deploy"},
			notExpect: []string{"Context:", "Target:", "Permission:"},
		},
		{
			name:       "no_fields",
			scope:      "",
			action:     "",
			operation:  "",
			target:     "",
			permission: "",
			expect:     []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatConsentDescription(
				tt.scope, tt.action, tt.operation,
				tt.target, tt.permission,
			)
			for _, s := range tt.expect {
				assert.Contains(t, result, s)
			}
			for _, s := range tt.notExpect {
				assert.NotContains(t, result, s)
			}
		})
	}
}

func TestFormatConsentRuleDescription(t *testing.T) {
	t.Parallel()

	rule := consent.ConsentRule{
		Scope:      "subscription",
		Action:     "provision",
		Operation:  "create",
		Target:     "resources",
		Permission: "admin",
	}

	result := formatConsentRuleDescription(rule)
	require.Contains(t, result, "Scope: subscription")
	require.Contains(t, result, "Action: provision")
	require.Contains(t, result, "Context: create")
	require.Contains(t, result, "Target: resources")
	require.Contains(t, result, "Permission: admin")
}
