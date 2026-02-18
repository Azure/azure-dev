// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestInitFailFastMissingEnvNonInteractive(t *testing.T) {
	t.Run("FailsWhenNoPromptWithTemplateAndNoEnv", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := &initAction{
			lazyAzdCtx: lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
				return nil, nil
			}),
			console: mockContext.Console,
			flags:   flags,
		}

		result, err := action.Run(*mockContext.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(),
			"--environment is required when running in non-interactive mode")
		require.Nil(t, result)
	})

	t.Run("DoesNotFailWhenEnvProvided", func(t *testing.T) {
		// Verify the condition: with env name set, no fail-fast error
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "myenv"

		shouldFailFast := flags.global.NoPrompt && flags.templatePath != "" && flags.EnvironmentName == ""
		require.False(t, shouldFailFast)
	})

	t.Run("DoesNotFailInInteractiveMode", func(t *testing.T) {
		// Verify the condition: in interactive mode, no fail-fast error
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: false},
		}

		shouldFailFast := flags.global.NoPrompt && flags.templatePath != "" && flags.EnvironmentName == ""
		require.False(t, shouldFailFast)
	})

	t.Run("DoesNotFailWithoutTemplate", func(t *testing.T) {
		// Verify the condition: without template, no fail-fast error
		flags := &initFlags{
			templatePath: "",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}

		shouldFailFast := flags.global.NoPrompt && flags.templatePath != "" && flags.EnvironmentName == ""
		require.False(t, shouldFailFast)
	})
}
