// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/stretchr/testify/require"
)

func Test_HooksRunAction_RespectInteractiveConfig(t *testing.T) {
	tests := []struct {
		name                string
		configInteractive   bool
		expectedInteractive bool
	}{
		{
			name:                "Interactive hook should remain interactive",
			configInteractive:   true,
			expectedInteractive: true,
		},
		{
			name:                "Non-interactive hook should remain non-interactive",
			configInteractive:   false,
			expectedInteractive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hooksAction := &hooksRunAction{
				flags: &hooksRunFlags{}, // Initialize flags to avoid nil pointer
			}

			// Create a hook config with the desired interactive setting
			hook := &ext.HookConfig{
				Interactive: tt.configInteractive,
				Shell:       ext.ShellTypeBash,
				Run:         "echo 'test'",
			}

			// Apply the prepare hook logic
			err := hooksAction.prepareHook("test", hook)
			require.NoError(t, err)

			// Verify that the Interactive setting was preserved
			require.Equal(t, tt.expectedInteractive, hook.Interactive,
				"Hook interactive setting should be preserved, got %t, expected %t",
				hook.Interactive, tt.expectedInteractive)
		})
	}
}
