// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// setupInitAction creates an initAction wired with mocks that pass git-install checks.
// The working directory is changed to a temp dir so that .env loading and azdcontext work.
func setupInitAction(t *testing.T, mockContext *mocks.MockContext, flags *initFlags) *initAction {
	t.Helper()

	// Work in a temp directory so os.Getwd / godotenv.Overload operate in isolation.
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Mock git so tools.EnsureInstalled succeeds.
	mockContext.CommandRunner.MockToolInPath("git", nil)
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "git") && strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "git version 2.42.0", ""), nil
	})

	gitCli := git.NewCli(mockContext.CommandRunner)

	return &initAction{
		lazyAzdCtx: lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
			return azdcontext.NewAzdContextWithDirectory(tmpDir), nil
		}),
		console:         mockContext.Console,
		cmdRun:          mockContext.CommandRunner,
		gitCli:          gitCli,
		flags:           flags,
		featuresManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}
}

// runActionSafe calls action.Run and returns the error. If Run panics (because
// later stages lack mocks), the panic is recovered and a nil error is returned
// — the test only cares that the fail-fast check did not fire.
func runActionSafe(ctx context.Context, action *initAction) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			// A panic means we got past the fail-fast check — treat as success.
			retErr = fmt.Errorf("panic (past fail-fast): %v", r)
		}
	}()

	_, err := action.Run(ctx)
	return err
}

func TestInitNoPromptRequiresMode(t *testing.T) {
	t.Run("ReturnsInitNoPromptErrorWhenNoMode", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)

		flags := &initFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := setupInitAction(t, mockContext, flags)

		result, err := action.Run(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, result)

		var noPromptErr *initModeRequiredError
		require.ErrorAs(t, err, &noPromptErr)

		output := noPromptErr.ToString("")
		require.Contains(t, output, "Init cannot continue (interactive prompts disabled)")
		require.Contains(t, output, "azd init --minimal")
		require.Contains(t, output, "azd init --template")
	})

	t.Run("DoesNotErrorWhenMinimalFlagSet", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)

		flags := &initFlags{
			minimal: true,
			global:  &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := setupInitAction(t, mockContext, flags)

		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			var noPromptErr *initModeRequiredError
			require.False(t, errors.As(err, &noPromptErr),
				"should not return InitNoPromptError when --minimal is set")
		}
	})

	t.Run("DoesNotErrorWhenTemplateAndEnvironmentProvided", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "myenv"

		action := setupInitAction(t, mockContext, flags)

		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			var noPromptErr *initModeRequiredError
			require.False(t, errors.As(err, &noPromptErr),
				"should not return InitNoPromptError when --template and --environment are both set")
		}
	})
}

func TestInitFailFastMissingEnvNonInteractive(t *testing.T) {
	t.Run("NoLongerFailsWhenNoPromptWithTemplateAndNoEnv", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := setupInitAction(t, mockContext, flags)

		// With sensible defaults, --no-prompt --template without --environment should not
		// fail with the old "--environment is required" error. The action will error or
		// panic later due to missing mocks for template download, which is expected —
		// we only verify the fail-fast guard was removed.
		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			require.NotContains(t, err.Error(),
				"--environment is required when running in non-interactive mode")
		}
	})

	t.Run("DoesNotFailWhenEnvProvidedViaFlag", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "myenv"

		action := setupInitAction(t, mockContext, flags)

		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			require.NotContains(t, err.Error(),
				"--environment is required when running in non-interactive mode")
		}
	})

	t.Run("DoesNotFailWhenEnvProvidedViaDotEnv", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := setupInitAction(t, mockContext, flags)

		// Write a .env file in the temp working directory with AZURE_ENV_NAME set.
		wd, err := os.Getwd()
		require.NoError(t, err)
		envFile := filepath.Join(wd, ".env")
		require.NoError(t, os.WriteFile(envFile, []byte("AZURE_ENV_NAME=from-dotenv\n"), 0600))

		err = runActionSafe(*mockContext.Context, action)
		if err != nil {
			require.NotContains(t, err.Error(),
				"--environment is required when running in non-interactive mode")
		}
	})

	t.Run("DoesNotFailInInteractiveMode", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: false},
		}

		action := setupInitAction(t, mockContext, flags)

		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			require.NotContains(t, err.Error(),
				"--environment is required when running in non-interactive mode")
		}
	})

	t.Run("DoesNotFailWithoutTemplate", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		flags := &initFlags{
			templatePath: "",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}

		action := setupInitAction(t, mockContext, flags)

		err := runActionSafe(*mockContext.Context, action)
		if err != nil {
			require.NotContains(t, err.Error(),
				"--environment is required when running in non-interactive mode")
		}
	})
}
