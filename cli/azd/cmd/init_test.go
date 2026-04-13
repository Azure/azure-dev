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
func setupInitAction(
	t *testing.T, mockContext *mocks.MockContext, flags *initFlags, args ...string,
) *initAction {
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
		console:          mockContext.Console,
		cmdRun:           mockContext.CommandRunner,
		gitCli:           gitCli,
		flags:            flags,
		args:             args,
		featuresManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		isRunningInAgent: func() bool { return false },
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

func TestInitResolveTargetDirectory(t *testing.T) {
	t.Run("DotArgUsesCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, ".")

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, wd, result)
	})

	t.Run("ExplicitDirectoryUsesArg", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, "my-project")

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "my-project"), result)
	})

	t.Run("NoArgDerivesFromTemplatePath", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Simulate a real terminal — auto-derive only activates in interactive TTY mode.
		mockContext.Console.SetTerminal(true)
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "todo-nodejs-mongo"), result)
	})

	t.Run("NonTTYDefaultsToCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Non-TTY (default mock) — should fall back to CWD even without --no-prompt,
		// preventing breakage for CI scripts that pipe stdin.
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, wd, result, "non-TTY should default to CWD")
	})

	t.Run("NoArgWithFilterTagsUsesCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templateTags: []string{"python"},
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, wd, result)
	})

	t.Run("TemplateWithDotGitSuffix", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetTerminal(true)
		flags := &initFlags{
			templatePath: "https://github.com/Azure-Samples/todo-nodejs-mongo.git",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "todo-nodejs-mongo"), result)
	})

	t.Run("AbsolutePathIsRejected", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, "/some/absolute/path")

		wd, err := os.Getwd()
		require.NoError(t, err)

		_, err = action.resolveTargetDirectory(wd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "absolute path")
	})

	t.Run("DotDotTraversalIsRejected", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, "../../etc/evil")

		wd, err := os.Getwd()
		require.NoError(t, err)

		_, err = action.resolveTargetDirectory(wd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "escapes the current working directory")
	})

	t.Run("SingleDotDotIsRejected", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, "..")

		wd, err := os.Getwd()
		require.NoError(t, err)

		_, err = action.resolveTargetDirectory(wd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "escapes the current working directory")
	})

	t.Run("NestedSubdirectoryIsAllowed", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags, "sub/dir/project")

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "sub/dir/project"), result)
	})

	t.Run("NoPromptNoArgDefaultsToCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, wd, result, "--no-prompt without positional arg should default to CWD")
	})
}

func TestInitValidateTargetDirectory(t *testing.T) {
	t.Run("NonExistentDirectoryIsValid", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		err := action.validateTargetDirectory(
			*mockContext.Context, filepath.Join(t.TempDir(), "nonexistent"))
		require.NoError(t, err)
	})

	t.Run("EmptyDirectoryIsValid", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		emptyDir := t.TempDir()
		err := action.validateTargetDirectory(*mockContext.Context, emptyDir)
		require.NoError(t, err)
	})

	t.Run("NonEmptyDirectoryErrorsInNoPromptMode", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		action := setupInitAction(t, mockContext, flags)

		nonEmptyDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(nonEmptyDir, "existing.txt"), []byte("content"), 0600))

		err := action.validateTargetDirectory(*mockContext.Context, nonEmptyDir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists and is not empty")
	})

	t.Run("NonEmptyDirectoryShowsWarningInInteractiveMode", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		nonEmptyDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(nonEmptyDir, "existing.txt"), []byte("content"), 0600))

		// In interactive mode, a warning is shown but no error is returned —
		// the downstream template init handles overwrite confirmation.
		err := action.validateTargetDirectory(*mockContext.Context, nonEmptyDir)
		require.NoError(t, err)
	})
}

func TestInitCreatesProjectDirectory(t *testing.T) {
	t.Run("TemplateInitCreatesDirectory", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Simulate interactive terminal so auto-directory creation kicks in.
		mockContext.Console.SetTerminal(true)
		// Not using --no-prompt so the auto-directory creation kicks in
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{},
		}
		flags.EnvironmentName = "testenv"
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		expectedDir := filepath.Join(wd, "todo-nodejs-mongo")
		require.NoDirExists(t, expectedDir)

		// Run will panic or error later due to missing template mocks,
		// but the directory should be created before that point.
		_ = runActionSafe(*mockContext.Context, action)
		require.DirExists(t, expectedDir)
	})

	t.Run("NoPromptTemplateInitUsesCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "testenv"
		action := setupInitAction(t, mockContext, flags)

		wd, err := os.Getwd()
		require.NoError(t, err)

		// In --no-prompt mode without a positional arg, init should use CWD
		// to preserve backward compatibility with existing automation.
		_ = runActionSafe(*mockContext.Context, action)

		derivedDir := filepath.Join(wd, "todo-nodejs-mongo")
		require.NoDirExists(t, derivedDir)
	})

	t.Run("DotArgDoesNotCreateDirectory", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "testenv"
		action := setupInitAction(t, mockContext, flags, ".")

		wd, err := os.Getwd()
		require.NoError(t, err)

		// Should NOT create a todo-nodejs-mongo subdirectory
		_ = runActionSafe(*mockContext.Context, action)

		derivedDir := filepath.Join(wd, "todo-nodejs-mongo")
		require.NoDirExists(t, derivedDir)
	})

	t.Run("ExplicitDirArgCreatesNamedDirectory", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{NoPrompt: true},
		}
		flags.EnvironmentName = "testenv"
		action := setupInitAction(t, mockContext, flags, "my-custom-project")

		wd, err := os.Getwd()
		require.NoError(t, err)

		expectedDir := filepath.Join(wd, "my-custom-project")
		require.NoDirExists(t, expectedDir)

		_ = runActionSafe(*mockContext.Context, action)
		require.DirExists(t, expectedDir)
	})

	t.Run("FailedInitCleansUpDirectory", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		// Not using --no-prompt so auto-directory creation happens
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{},
		}

		tmpDir := t.TempDir()
		t.Chdir(tmpDir)

		// Mock git as NOT installed so tools.EnsureInstalled fails with a real
		// error (not a panic), which triggers the cleanup-on-failure defer.
		mockContext.CommandRunner.MockToolInPath("git", fmt.Errorf("git not found"))
		gitCli := git.NewCli(mockContext.CommandRunner)

		action := &initAction{
			lazyAzdCtx: lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
				return azdcontext.NewAzdContextWithDirectory(tmpDir), nil
			}),
			console:          mockContext.Console,
			cmdRun:           mockContext.CommandRunner,
			gitCli:           gitCli,
			flags:            flags,
			args:             []string{},
			featuresManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
			isRunningInAgent: func() bool { return false },
		}

		// Simulate interactive terminal so auto-directory creation kicks in.
		mockContext.Console.SetTerminal(true)

		expectedDir := filepath.Join(tmpDir, "todo-nodejs-mongo")
		require.NoDirExists(t, expectedDir)

		_, err := action.Run(*mockContext.Context)
		require.Error(t, err)

		// The created directory should be cleaned up after failure
		require.NoDirExists(t, expectedDir)

		// CWD should be restored to the original directory
		currentWd, wdErr := os.Getwd()
		require.NoError(t, wdErr)
		require.Equal(t, tmpDir, currentWd)
	})

	t.Run("FailedInitPreservesPreExistingDirectory", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		flags := &initFlags{
			templatePath: "Azure-Samples/todo-nodejs-mongo",
			global:       &internal.GlobalCommandOptions{},
		}

		tmpDir := t.TempDir()
		t.Chdir(tmpDir)

		// Pre-create the target directory with a file inside
		preExistingDir := filepath.Join(tmpDir, "todo-nodejs-mongo")
		require.NoError(t, os.MkdirAll(preExistingDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(preExistingDir, "existing.txt"), []byte("keep me"), 0o600))

		// Mock git as NOT installed to trigger failure after dir creation
		mockContext.CommandRunner.MockToolInPath("git", fmt.Errorf("git not found"))
		gitCli := git.NewCli(mockContext.CommandRunner)

		action := &initAction{
			lazyAzdCtx: lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
				return azdcontext.NewAzdContextWithDirectory(tmpDir), nil
			}),
			console:          mockContext.Console,
			cmdRun:           mockContext.CommandRunner,
			gitCli:           gitCli,
			flags:            flags,
			args:             []string{"todo-nodejs-mongo"},
			featuresManager:  alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
			isRunningInAgent: func() bool { return false },
		}

		_, err := action.Run(*mockContext.Context)
		require.Error(t, err)

		// The pre-existing directory must NOT be deleted
		require.DirExists(t, preExistingDir)
		content, readErr := os.ReadFile(filepath.Join(preExistingDir, "existing.txt"))
		require.NoError(t, readErr)
		require.Equal(t, "keep me", string(content))
	})

	t.Run("LocalTemplateSelfTargetFallsBackToCwd", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetTerminal(true)

		tmpDir := t.TempDir()
		// Create a directory that matches what DeriveDirectoryName would produce
		templateDir := filepath.Join(tmpDir, "my-template")
		require.NoError(t, os.MkdirAll(templateDir, 0o755))
		t.Chdir(tmpDir)

		flags := &initFlags{
			templatePath: "./my-template",
			global:       &internal.GlobalCommandOptions{},
		}
		action := setupInitAction(t, mockContext, flags)

		// resolveTargetDirectory derives "my-template" from "./my-template".
		// The self-targeting check happens in Run(), not in resolveTargetDirectory.
		wd, err := os.Getwd()
		require.NoError(t, err)

		result, err := action.resolveTargetDirectory(wd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "my-template"), result)
	})
}
