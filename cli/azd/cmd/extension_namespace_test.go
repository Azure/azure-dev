// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestNamespacesConflict(t *testing.T) {
	tests := []struct {
		name     string
		ns1      string
		ns2      string
		conflict bool
		msg      string
	}{
		// Exact match
		{
			name:     "same namespace",
			ns1:      "ai",
			ns2:      "ai",
			conflict: true,
			msg:      "the same namespace",
		},
		// Overlapping namespaces (one is prefix of other)
		{
			name:     "ns1 is prefix of ns2",
			ns1:      "ai",
			ns2:      "ai.agent",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		{
			name:     "ns2 is prefix of ns1",
			ns1:      "ai.agent",
			ns2:      "ai",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		{
			name:     "deeply nested overlap",
			ns1:      "ai.models",
			ns2:      "ai.models.finetune",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		// Case-insensitive comparisons
		{
			name:     "case insensitive - same namespace",
			ns1:      "AI",
			ns2:      "ai",
			conflict: true,
			msg:      "the same namespace",
		},
		{
			name:     "case insensitive - ns1 prefix uppercase",
			ns1:      "AI",
			ns2:      "ai.agent",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		{
			name:     "case insensitive - ns2 prefix uppercase",
			ns1:      "ai.agent",
			ns2:      "AI",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		{
			name:     "case insensitive - mixed case",
			ns1:      "Ai.Agent",
			ns2:      "AI.AGENT.Sub",
			conflict: true,
			msg:      "overlapping namespaces",
		},
		// No conflict cases
		{
			name:     "no conflict - different namespaces",
			ns1:      "ai",
			ns2:      "demo",
			conflict: false,
		},
		{
			name:     "no conflict - sibling namespaces",
			ns1:      "ai.agent",
			ns2:      "ai.finetune",
			conflict: false,
		},
		{
			name:     "no conflict - partial match not at boundary",
			ns1:      "ai",
			ns2:      "air",
			conflict: false,
		},
		{
			name:     "no conflict - similar prefix but not exact",
			ns1:      "ai.agent",
			ns2:      "ai.agents",
			conflict: false,
		},
		{
			name:     "no conflict - case insensitive siblings",
			ns1:      "AI.Agent",
			ns2:      "ai.finetune",
			conflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflict, msg := namespacesConflict(tt.ns1, tt.ns2)
			require.Equal(t, tt.conflict, conflict)
			if tt.conflict {
				require.Equal(t, tt.msg, msg)
			}
		})
	}
}

func TestCheckNamespaceConflict(t *testing.T) {
	t.Run("no conflict with empty installed extensions", func(t *testing.T) {
		err := checkNamespaceConflict("new.ext", "demo", map[string]*extensions.Extension{})
		require.NoError(t, err)
	})

	t.Run("no conflict with different namespaces", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"existing.ext": {Id: "existing.ext", Namespace: "other"},
		}
		err := checkNamespaceConflict("new.ext", "demo", installed)
		require.NoError(t, err)
	})

	t.Run("conflict with installed extension - same namespace", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"existing.ext": {Id: "existing.ext", Namespace: "demo"},
		}
		err := checkNamespaceConflict("new.ext", "demo", installed)
		require.Error(t, err)

		// Verify it's an ErrorWithSuggestion
		var errWithSuggestion *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &errWithSuggestion)
		require.Contains(t, errWithSuggestion.Err.Error(), "conflicts with installed extension")
		require.Contains(t, errWithSuggestion.Err.Error(), "existing.ext")
		require.Contains(t, errWithSuggestion.Suggestion, "Uninstall")
	})

	t.Run("conflict with installed extension - overlapping namespace", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"microsoft.azd.ai.builder": {Id: "microsoft.azd.ai.builder", Namespace: "ai"},
		}
		err := checkNamespaceConflict("azure.ai.agents", "ai.agent", installed)
		require.Error(t, err)

		var errWithSuggestion *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &errWithSuggestion)
		require.Contains(t, errWithSuggestion.Err.Error(), "conflicts with installed extension")
		require.Contains(t, errWithSuggestion.Err.Error(), "microsoft.azd.ai.builder")
	})

	t.Run("no conflict when checking against self (upgrade case)", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"my.ext": {Id: "my.ext", Namespace: "demo"},
		}
		err := checkNamespaceConflict("my.ext", "demo", installed)
		require.NoError(t, err)
	})

	t.Run("no conflict with empty namespace for new extension", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"existing.ext": {Id: "existing.ext", Namespace: "demo"},
		}
		err := checkNamespaceConflict("new.ext", "", installed)
		require.NoError(t, err)
	})

	t.Run("skips installed extensions with empty namespace", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"existing.ext": {Id: "existing.ext", Namespace: ""},
		}
		err := checkNamespaceConflict("new.ext", "demo", installed)
		require.NoError(t, err)
	})

	t.Run("case insensitive conflict detection", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"existing.ext": {Id: "existing.ext", Namespace: "AI.Agent"},
		}
		err := checkNamespaceConflict("new.ext", "ai", installed)
		require.Error(t, err)

		var errWithSuggestion *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &errWithSuggestion)
	})

	t.Run("multiple installed - finds conflict", func(t *testing.T) {
		installed := map[string]*extensions.Extension{
			"safe.ext":        {Id: "safe.ext", Namespace: "demo"},
			"conflicting.ext": {Id: "conflicting.ext", Namespace: "ai.models"},
			"another.ext":     {Id: "another.ext", Namespace: "tools"},
		}
		err := checkNamespaceConflict("new.ext", "ai", installed)
		require.Error(t, err)

		var errWithSuggestion *internal.ErrorWithSuggestion
		require.ErrorAs(t, err, &errWithSuggestion)
		require.Contains(t, errWithSuggestion.Err.Error(), "conflicting.ext")
	})
}
