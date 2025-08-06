// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package powershell

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Powershell_Execute(t *testing.T) {
	workingDir := "cwd"
	scriptPath := "path/script.ps1"
	env := []string{
		"a=apple",
		"b=banana",
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// #nosec G101
		userPwsh := "pwsh -NoProfile"
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(args.Cmd, userPwsh)
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			require.Equal(t, userPwsh, args.Cmd)
			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)

			return exec.NewRunResult(0, "", ""), nil
		})

		PowershellScript := NewPowershellScriptWithMockCheckPath(
			mockContext.CommandRunner,
			workingDir,
			env,
			func(options tools.ExecOptions, enableFallback bool) (string, error) {
				return options.UserPwsh, nil
			},
			nil)
		runResult, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{UserPwsh: userPwsh, Interactive: to.Ptr(true)},
		)

		require.NotNil(t, runResult)
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return true
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error message"), errors.New("error message")
		})

		PowershellScript := NewPowershellScriptWithMockCheckPath(
			mockContext.CommandRunner,
			workingDir,
			env,
			func(options tools.ExecOptions, enableFallback bool) (string, error) {
				return options.UserPwsh, nil
			},
			nil)
		runResult, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: to.Ptr(true)},
		)

		require.Equal(t, 1, runResult.ExitCode)
		require.Error(t, err)
	})

	t.Run("NoPowerShellInstalled", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		PowershellScript := NewPowershellScript(mockContext.CommandRunner, workingDir, env, nil)
		_, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{Interactive: to.Ptr(true)},
		)

		require.Error(t, err)
	})

	tests := []struct {
		name  string
		value tools.ExecOptions
	}{
		{name: "Interactive", value: tools.ExecOptions{Interactive: to.Ptr(true)}},
		{name: "NonInteractive", value: tools.ExecOptions{Interactive: to.Ptr(false)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())

			mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				require.Equal(t, *test.value.Interactive, args.Interactive)
				return exec.NewRunResult(0, "", ""), nil
			})

			PowershellScript := NewPowershellScriptWithMockCheckPath(
				mockContext.CommandRunner,
				workingDir,
				env,
				func(options tools.ExecOptions, enableFallback bool) (string, error) {
					return options.UserPwsh, nil
				},
				nil)
			runResult, err := PowershellScript.Execute(*mockContext.Context, scriptPath, test.value)

			require.NotNil(t, runResult)
			require.NoError(t, err)
		})
	}
}

func Test_Alpha_Feature_Detection(t *testing.T) {
	t.Run("CheckIfFeatureExistsInRealFeaturesList", func(t *testing.T) {
		// Check if our feature exists in the features list
		featureId, isValidFeature := alpha.IsFeatureKey("powershell.fallback5")
		require.True(t, isValidFeature, "powershell.fallback5 should be a valid alpha feature")
		require.Equal(t, alpha.FeatureId("powershell.fallback5"), featureId)
	})

	t.Run("FeatureEnabled", func(t *testing.T) {
		mockConfig := config.NewConfig(nil)
		err := mockConfig.Set("alpha.powershell.fallback5", "on")
		require.NoError(t, err)

		alphaManager := alpha.NewFeaturesManagerWithConfig(mockConfig)

		enabled := alphaManager.IsEnabled(alpha.FeatureId("powershell.fallback5"))
		require.True(t, enabled, "powershell.fallback5 feature should be enabled")
	})

	t.Run("FeatureDisabled", func(t *testing.T) {
		alphaManager := alpha.NewFeaturesManagerWithConfig(config.NewConfig(nil))

		enabled := alphaManager.IsEnabled(alpha.FeatureId("powershell.fallback5"))
		require.False(t, enabled, "powershell.fallback5 feature should be disabled by default")
	})
}

func Test_Powershell_Fallback_To_PowerShell5(t *testing.T) {
	workingDir := "cwd"
	scriptPath := "path/script.ps1"
	env := []string{"a=apple", "b=banana"}

	t.Run("FallbackEnabled_PwshNotAvailable_PowerShellAvailable", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Create a mock config that enables the fallback feature
		mockConfig := config.NewConfig(nil)
		err := mockConfig.Set("alpha.powershell.fallback5", "on")
		require.NoError(t, err)
		alphaManager := alpha.NewFeaturesManagerWithConfig(mockConfig)

		// Mock command runner to expect 'powershell' command
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(args.Cmd, "powershell")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			require.Equal(t, "powershell", args.Cmd)
			require.Equal(t, workingDir, args.Cwd)
			require.Equal(t, scriptPath, args.Args[0])
			require.Equal(t, env, args.Env)
			return exec.NewRunResult(0, "", ""), nil
		})

		PowershellScript := NewPowershellScriptWithMockCheckPath(
			mockContext.CommandRunner,
			workingDir,
			env,
			func(options tools.ExecOptions, enableFallback bool) (string, error) {
				// Simulate pwsh not available, but powershell is available
				if enableFallback {
					return "powershell", nil
				}
				return "", errors.New("pwsh not found")
			},
			alphaManager)

		runResult, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{UserPwsh: "pwsh", Interactive: to.Ptr(true)},
		)

		require.NotNil(t, runResult)
		require.NoError(t, err)
	})

	t.Run("FallbackDisabled_PwshNotAvailable_ShouldFail", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// No alpha config, so fallback is disabled
		alphaManager := alpha.NewFeaturesManagerWithConfig(config.NewConfig(nil))

		PowershellScript := NewPowershellScriptWithMockCheckPath(
			mockContext.CommandRunner,
			workingDir,
			env,
			func(options tools.ExecOptions, enableFallback bool) (string, error) {
				// Simulate pwsh not available and fallback disabled
				require.False(t, enableFallback)
				return "", errors.New("pwsh not found")
			},
			alphaManager)

		_, err := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{UserPwsh: "pwsh", Interactive: to.Ptr(true)},
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "pwsh not found")
	})

	t.Run("FallbackEnabled_BothNotAvailable_ShouldFail", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		// Enable fallback feature
		mockConfig := config.NewConfig(nil)
		err := mockConfig.Set("alpha.powershell.fallback5", "on")
		require.NoError(t, err)
		alphaManager := alpha.NewFeaturesManagerWithConfig(mockConfig)

		PowershellScript := NewPowershellScriptWithMockCheckPath(
			mockContext.CommandRunner,
			workingDir,
			env,
			func(options tools.ExecOptions, enableFallback bool) (string, error) {
				// Simulate both pwsh and powershell not available
				require.True(t, enableFallback)
				return "", errors.New("neither pwsh nor powershell found")
			},
			alphaManager)

		_, execErr := PowershellScript.Execute(
			*mockContext.Context,
			scriptPath,
			tools.ExecOptions{UserPwsh: "pwsh", Interactive: to.Ptr(true)},
		)

		require.Error(t, execErr)
		require.Contains(t, execErr.Error(), "neither pwsh nor powershell found")
	})
}

func Test_Powershell_Integration_CheckPathFunction(t *testing.T) {
	// Test the actual checkPath function behavior
	t.Run("FallbackDisabled_PwshAvailable", func(t *testing.T) {
		options := tools.ExecOptions{UserPwsh: "pwsh"}
		// This may fail in environments where pwsh is not installed, which is expected
		command, err := checkPath(options, false)
		if err == nil {
			require.Equal(t, "pwsh", command)
		}
		// If pwsh is not available, the function should return an error
	})

	t.Run("FallbackEnabled_BothAvailable_ShouldPreferPwsh", func(t *testing.T) {
		options := tools.ExecOptions{UserPwsh: "pwsh"}
		// This may fail in environments where pwsh is not installed
		command, err := checkPath(options, true)
		if err == nil {
			require.Equal(t, "pwsh", command)
		}
		// If neither is available, the function should return an error
	})
}
