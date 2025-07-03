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
		configInteractive   *bool
		expectedInteractive bool
	}{
		{
			name:                "Interactive hook should remain interactive",
			configInteractive:   ext.BoolPtr(true),
			expectedInteractive: true,
		},
		{
			name:                "Non-interactive hook should remain non-interactive",
			configInteractive:   ext.BoolPtr(false),
			expectedInteractive: false,
		},
		{
			name:                "Unset interactive should default to false",
			configInteractive:   nil,
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
			actualInteractive := ext.GetBoolValue(hook.Interactive, false)
			require.Equal(t, tt.expectedInteractive, actualInteractive,
				"Hook interactive setting should be preserved, got %t, expected %t",
				actualInteractive, tt.expectedInteractive)
		})
	}
}

func Test_MergeHookConfig_InteractivePrecedence(t *testing.T) {
	tests := []struct {
		name                  string
		parentInteractive     *bool
		osSpecificInteractive *bool
		expectedInteractive   bool
	}{
		{
			name:                  "OS-specific false overrides parent true",
			parentInteractive:     ext.BoolPtr(true),
			osSpecificInteractive: ext.BoolPtr(false),
			expectedInteractive:   false,
		},
		{
			name:                  "OS-specific true overrides parent false",
			parentInteractive:     ext.BoolPtr(false),
			osSpecificInteractive: ext.BoolPtr(true),
			expectedInteractive:   true,
		},
		{
			name:                  "OS-specific unset inherits parent true",
			parentInteractive:     ext.BoolPtr(true),
			osSpecificInteractive: nil,
			expectedInteractive:   true,
		},
		{
			name:                  "OS-specific unset inherits parent false",
			parentInteractive:     ext.BoolPtr(false),
			osSpecificInteractive: nil,
			expectedInteractive:   false,
		},
		{
			name:                  "Both unset defaults to false",
			parentInteractive:     nil,
			osSpecificInteractive: nil,
			expectedInteractive:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &ext.HookConfig{
				Interactive: tt.parentInteractive,
				Shell:       ext.ShellTypeBash,
				Run:         "echo 'parent'",
			}

			osSpecific := &ext.HookConfig{
				Interactive: tt.osSpecificInteractive,
				Shell:       ext.ShellTypeBash,
				Run:         "echo 'os-specific'",
			}

			merged := ext.MergeHookConfig(parent, osSpecific)

			actualInteractive := ext.GetBoolValue(merged.Interactive, false)
			require.Equal(t, tt.expectedInteractive, actualInteractive,
				"Merged interactive setting should match expected, got %t, expected %t",
				actualInteractive, tt.expectedInteractive)
		})
	}
}

func Test_MergeHookConfig_ContinueOnErrorPrecedence(t *testing.T) {
	tests := []struct {
		name                      string
		parentContinueOnError     *bool
		osSpecificContinueOnError *bool
		expectedContinueOnError   bool
	}{
		{
			name:                      "OS-specific false overrides parent true",
			parentContinueOnError:     ext.BoolPtr(true),
			osSpecificContinueOnError: ext.BoolPtr(false),
			expectedContinueOnError:   false,
		},
		{
			name:                      "OS-specific true overrides parent false",
			parentContinueOnError:     ext.BoolPtr(false),
			osSpecificContinueOnError: ext.BoolPtr(true),
			expectedContinueOnError:   true,
		},
		{
			name:                      "OS-specific unset inherits parent true",
			parentContinueOnError:     ext.BoolPtr(true),
			osSpecificContinueOnError: nil,
			expectedContinueOnError:   true,
		},
		{
			name:                      "OS-specific unset inherits parent false",
			parentContinueOnError:     ext.BoolPtr(false),
			osSpecificContinueOnError: nil,
			expectedContinueOnError:   false,
		},
		{
			name:                      "Both unset defaults to false",
			parentContinueOnError:     nil,
			osSpecificContinueOnError: nil,
			expectedContinueOnError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &ext.HookConfig{
				ContinueOnError: tt.parentContinueOnError,
				Shell:           ext.ShellTypeBash,
				Run:             "echo 'parent'",
			}

			osSpecific := &ext.HookConfig{
				ContinueOnError: tt.osSpecificContinueOnError,
				Shell:           ext.ShellTypeBash,
				Run:             "echo 'os-specific'",
			}

			merged := ext.MergeHookConfig(parent, osSpecific)

			actualContinueOnError := ext.GetBoolValue(merged.ContinueOnError, false)
			require.Equal(t, tt.expectedContinueOnError, actualContinueOnError,
				"Merged continueOnError setting should match expected, got %t, expected %t",
				actualContinueOnError, tt.expectedContinueOnError)
		})
	}
}
