// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())

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
		mockContext := mocks.NewMockContext(t.Context())

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
		mockContext := mocks.NewMockContext(t.Context())

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
		mockContext := mocks.NewMockContext(t.Context())

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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{
			templatePath: "owner/repo",
			global:       &internal.GlobalCommandOptions{},
		}
		// Use a platform-appropriate absolute path so the test works on Windows too.
		absPath := filepath.Join(os.TempDir(), "some", "absolute", "path")
		action := setupInitAction(t, mockContext, flags, absPath)

		wd, err := os.Getwd()
		require.NoError(t, err)

		_, err = action.resolveTargetDirectory(wd)
		require.Error(t, err)
		require.Contains(t, err.Error(), "absolute path")
	})

	t.Run("DotDotTraversalIsRejected", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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
		mockContext := mocks.NewMockContext(t.Context())
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

func Test_OutputFormatters(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}

	jsonFmt := &output.JsonFormatter{}
	err := jsonFmt.Format(map[string]string{"k": "v"}, buf, nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "k")

	noneFmt := &output.NoneFormatter{}
	err = noneFmt.Format("data", buf, nil)
	require.Error(t, err)
}

func Test_NewInitAction(t *testing.T) {
	t.Parallel()
	action := newInitAction(
		nil, // lazyAzdCtx
		nil, // lazyEnvManager
		nil, // cmdRun
		mockinput.NewMockConsole(),
		nil, // gitCli
		&initFlags{},
		nil, // args
		nil, // repoInitializer
		nil, // templateManager
		nil, // featuresManager
		nil, // extensionsManager
		nil, // azd
		nil, // agentFactory
		nil, // consentManager
		nil, // configManager
	)
	require.NotNil(t, action)
}

func Test_ConsentParsers(t *testing.T) {
	t.Parallel()

	t.Run("ParseActionType", func(t *testing.T) {
		t.Parallel()
		at, err := consent.ParseActionType("all")
		require.NoError(t, err)
		require.Equal(t, consent.ActionAny, at)

		at, err = consent.ParseActionType("readonly")
		require.NoError(t, err)
		require.Equal(t, consent.ActionReadOnly, at)

		_, err = consent.ParseActionType("invalid")
		require.Error(t, err)
	})

	t.Run("ParseOperationType", func(t *testing.T) {
		t.Parallel()
		ot, err := consent.ParseOperationType("tool")
		require.NoError(t, err)
		require.Equal(t, consent.OperationTypeTool, ot)

		_, err = consent.ParseOperationType("invalid")
		require.Error(t, err)
	})

	t.Run("ParsePermission", func(t *testing.T) {
		t.Parallel()
		p, err := consent.ParsePermission("allow")
		require.NoError(t, err)
		require.Equal(t, consent.PermissionAllow, p)

		_, err = consent.ParsePermission("invalid")
		require.Error(t, err)
	})

	t.Run("ParseScope", func(t *testing.T) {
		t.Parallel()
		s, err := consent.ParseScope("global")
		require.NoError(t, err)
		require.Equal(t, consent.ScopeGlobal, s)

		s, err = consent.ParseScope("project")
		require.NoError(t, err)
		require.Equal(t, consent.Scope("project"), s)

		_, err = consent.ParseScope("invalid")
		require.Error(t, err)
	})
}

type mockKeyVaultService struct {
	mock.Mock
}

func (m *mockKeyVaultService) GetKeyVault(
	ctx context.Context, subscriptionId string, resourceGroupName string, vaultName string,
) (*keyvault.KeyVault, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, vaultName)
	return args.Get(0).(*keyvault.KeyVault), args.Error(1)
}

func (m *mockKeyVaultService) GetKeyVaultSecret(
	ctx context.Context, subscriptionId string, vaultName string, secretName string,
) (*keyvault.Secret, error) {
	args := m.Called(ctx, subscriptionId, vaultName, secretName)
	return args.Get(0).(*keyvault.Secret), args.Error(1)
}

func (m *mockKeyVaultService) PurgeKeyVault(
	ctx context.Context, subscriptionId string, vaultName string, location string,
) error {
	args := m.Called(ctx, subscriptionId, vaultName, location)
	return args.Error(0)
}

func (m *mockKeyVaultService) ListSubscriptionVaults(
	ctx context.Context, subscriptionId string,
) ([]keyvault.Vault, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]keyvault.Vault), args.Error(1)
}

func (m *mockKeyVaultService) CreateVault(
	ctx context.Context, tenantId string, subscriptionId string,
	resourceGroupName string, location string, vaultName string,
) (keyvault.Vault, error) {
	args := m.Called(ctx, tenantId, subscriptionId, resourceGroupName, location, vaultName)
	return args.Get(0).(keyvault.Vault), args.Error(1)
}

func (m *mockKeyVaultService) ListKeyVaultSecrets(
	ctx context.Context, subscriptionId string, vaultName string,
) ([]string, error) {
	args := m.Called(ctx, subscriptionId, vaultName)
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockKeyVaultService) CreateKeyVaultSecret(
	ctx context.Context, subscriptionId string, vaultName string, secretName string, value string,
) error {
	args := m.Called(ctx, subscriptionId, vaultName, secretName, value)
	return args.Error(0)
}

func (m *mockKeyVaultService) SecretFromAkvs(
	ctx context.Context, akvs string,
) (string, error) {
	args := m.Called(ctx, akvs)
	return args.String(0), args.Error(1)
}

func (m *mockKeyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, kvRef string, defaultSubscriptionId string,
) (string, error) {
	args := m.Called(ctx, kvRef, defaultSubscriptionId)
	return args.String(0), args.Error(1)
}

type mockPrompter struct {
	mock.Mock
}

func (m *mockPrompter) PromptSubscription(ctx context.Context, msg string) (string, error) {
	args := m.Called(ctx, msg)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptLocation(
	ctx context.Context, subId string, msg string,
	filter prompt.LocationFilterPredicate, defaultLocation *string,
) (string, error) {
	args := m.Called(ctx, subId, msg, filter, defaultLocation)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptResourceGroup(
	ctx context.Context, options prompt.PromptResourceOptions,
) (string, error) {
	args := m.Called(ctx, options)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptResourceGroupFrom(
	ctx context.Context, subscriptionId string, location string,
	options prompt.PromptResourceGroupFromOptions,
) (string, error) {
	args := m.Called(ctx, subscriptionId, location, options)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) IsNoPromptMode() bool {
	return false
}

type mockEnvSetSecretSubscriptionResolver struct {
	mock.Mock
}

func (m *mockEnvSetSecretSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	args := m.Called(ctx, subscriptionId)

	if subscription, ok := args.Get(0).(*account.Subscription); ok {
		return subscription, args.Error(1)
	}

	if tenantId, ok := args.Get(0).(string); ok {
		return &account.Subscription{
			Id:                 subscriptionId,
			TenantId:           tenantId,
			UserAccessTenantId: tenantId,
		}, args.Error(1)
	}

	return nil, args.Error(1)
}

type staticSubscriptionResolver struct {
	subscription *account.Subscription
}

func (s *staticSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	return s.subscription, nil
}

type mockEnvSetSecretEntraIdService struct {
	entraid.EntraIdService
	subscriptionId string
	scope          string
	roleId         string
	principalId    string
}

func (m *mockEnvSetSecretEntraIdService) CreateRbac(
	ctx context.Context, subscriptionId string, scope, roleId, principalId string,
) error {
	m.subscriptionId = subscriptionId
	m.scope = scope
	m.roleId = roleId
	m.principalId = principalId
	return nil
}

func Test_EnvSetSecretAction_SelectStrategyError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select cancelled")
	})

	env := environment.NewWithValues("test", map[string]string{})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting secret setting strategy")
}

func Test_EnvSetSecretAction_InvalidVaultId(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy (create new)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": "not-a-valid-resource-id",
	})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing key vault resource id")
}

func Test_EnvSetSecretAction_ProjectKV_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: project KV prompt
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("cancelled")
	})

	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting key vault option")
}

func Test_EnvSetSecretAction_ProjectKV_UseExisting_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy (create new = 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: use different KV (No = 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).Respond(1)

	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("no subscriptions"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_Cancel(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: "Cancel" = index 1
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation canceled by user")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select error")
	})

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting key vault option")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_UseDifferent_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).Respond(0) // Use a different key vault

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("no sub"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_NoProject_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("cancelled"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_LookupTenantError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("", fmt.Errorf("tenant not found"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting subscription")
}

func Test_EnvSetSecretAction_ListVaultsError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, fmt.Errorf("network error"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting the list of Key Vaults")
}

func Test_EnvSetSecretAction_SelectExisting_NoVaults(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Select existing strategy (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)
	// After discovering no vaults, it switches to create new and prompts for KV selection
	// The message keeps "where the Key Vault secret is" from the original !willCreateNewSecret path
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where the Key Vault secret is"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("cancelled")
	})

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, nil) // Empty list

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	// The error could be from Select or from a subsequent step
}

func Test_EnvSetSecretAction_SelectKVError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	// KV selection prompt error
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select kv error")
	})

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{{Name: "vault1", Id: "id1"}}, nil)

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting Key Vault")
}

func Test_EnvSetSecretAction_CreateNewKV_LocationError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0) // Create new

	// KV selection: pick "Create a new Key Vault" (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)
	prompter.On("PromptLocation", mock.Anything, "sub-123", mock.Anything, mock.Anything, mock.Anything).
		Return("", fmt.Errorf("location error"))

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, nil) // No existing vaults

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for Key Vault location")
}

func Test_EnvSetSecretAction_ProjectKV_UseExisting_CreateNewSecret(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Strategy: select existing (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)
	// Project KV: Yes (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).Respond(0)

	// selectKeyVaultSecret needs ListKeyVaultSecrets + Select for secret
	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListKeyVaultSecrets", mock.Anything, "sub123", "myvault").
		Return([]string{}, fmt.Errorf("list secrets error"))

	envMgr := &mockenv.MockEnvManager{}

	action := newTestEnvSetSecretAction(console, env, envMgr, []string{"mySecret"}, nil, kvSvc, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list secrets error")
}

func Test_ConfigSetAction_SaveError(t *testing.T) {
	t.Parallel()
	cfgMgr := &testConfigManager{
		loadCfg: config.NewEmptyConfig(),
		saveErr: fmt.Errorf("save failed"),
	}
	action := &configSetAction{
		configManager: cfgMgr,
		args:          []string{"key1", "value1"},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save failed")
}

// testConfigManager implements config.UserConfigManager for testing
type testConfigManager struct {
	loadCfg config.Config
	loadErr error
	saveErr error
}

func (m *testConfigManager) Load() (config.Config, error) {
	return m.loadCfg, m.loadErr
}

func (m *testConfigManager) Save(cfg config.Config) error {
	return m.saveErr
}

// setDefaultEnvHelper sets the default environment in the AzdContext
func setDefaultEnvHelper(t *testing.T, azdCtx *azdcontext.AzdContext, envName string) {
	t.Helper()
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{
		DefaultEnvironment: envName,
	}))
}

func Test_EnvSetSecretAction_SelectExisting_VaultListError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Select existing (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, fmt.Errorf("vault list error"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting the list of Key Vaults")
}

func Test_EnvSetSecretAction_CreateNew_ExistingVault_ListSecretsError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Strategy: create new (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// KV selection: pick existing vault (index 1, after "Create new" option)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockEnvSetSecretSubscriptionResolver{}
	resolver.On("GetSubscription", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{{Name: "vault1", Id: "id1"}}, nil)
	kvSvc.On("CreateKeyVaultSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("create secret error"))

	// The createNewKeyVaultSecret method prompts for secret name and value
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true // accept any prompt
	}).Respond("my-secret-value")

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ErrorWithSuggestion_Type(t *testing.T) {
	t.Parallel()
	err := &internal.ErrorWithSuggestion{
		Err:        internal.ErrNoArgsProvided,
		Suggestion: "test suggestion",
	}
	assert.True(t, errors.Is(err, internal.ErrNoArgsProvided))
	assert.Contains(t, err.Error(), "required arguments not provided")
}

func Test_NewEnvRemoveCmd_ArgsConflict(t *testing.T) {
	cmd := newEnvRemoveCmd()
	cmd.Flags().String(internal.EnvironmentNameFlagName, "", "")
	_ = cmd.Flags().Set(internal.EnvironmentNameFlagName, "other-env")

	err := cmd.Args(cmd, []string{"my-env"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may not be used together")
}

func Test_GetTargetServiceName_AllAndService_Conflict(t *testing.T) {
	t.Parallel()
	_, err := getTargetServiceName(t.Context(), nil, nil, nil, "build", "myservice", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot specify both --all and <service>")
}

func Test_ErrorWithSuggestion_Error(t *testing.T) {
	t.Parallel()
	err := &internal.ErrorWithSuggestion{
		Err:        fmt.Errorf("test error"),
		Suggestion: "try again",
	}
	assert.Contains(t, err.Error(), "test error")
}

func Test_UpdateAction_Run_SaveConfigError(t *testing.T) {
	// Tests the config save failure path when auto-enabling alpha
	setProdVersion(t)
	clearCIEnv(t)

	cfgMgr := &failSaveConfigMgr{}
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer

	flags := &updateFlags{
		channel:            "",
		checkIntervalHours: 12,
	}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save failed")
}

func Test_UpdateAction_Run_CI_Blocked(t *testing.T) {
	// Tests the CI block path
	setProdVersion(t)

	// Set CI=true so IsRunningOnCI returns true
	t.Setenv("CI", "true")

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("alpha.update", "on")
	cfgMgr := &simpleConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer

	flags := &updateFlags{}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "CI/CD")
}

func Test_NewEnvRefreshCmd_Args_ConflictingFlag(t *testing.T) {
	t.Parallel()
	cmd := newEnvRefreshCmd()
	cmd.Flags().String(internal.EnvironmentNameFlagName, "", "")
	// Set the flag to a different value than the arg
	require.NoError(t, cmd.Flags().Set(internal.EnvironmentNameFlagName, "flagenv"))
	err := cmd.Args(cmd, []string{"argenv"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "may not be used together")
}

// Test the "not provisioned yet" path
func Test_EnvSetSecretAction_VaultDefinedButNotProvisioned(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()

	selectCount := 0
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		selectCount++
		switch selectCount {
		case 1:
			// Strategy: Create new
			return 0, nil
		case 2:
			// "Cancel" (index 1)
			return 1, nil
		default:
			return 0, errors.New("unexpected select")
		}
	})

	env := environment.NewWithValues("myenv", nil)
	// projectConfig has vault resource but no AZURE_RESOURCE_VAULT_ID in env
	pc := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}

	action := &envSetSecretAction{
		args:          []string{"MY_SECRET"},
		console:       console,
		env:           env,
		projectConfig: pc,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "canceled")
}

func Test_SelectDistinctExtension_ZeroMatches(t *testing.T) {
	t.Parallel()
	_, err := selectDistinctExtension(
		t.Context(), mockinput.NewMockConsole(),
		"test-ext", []*extensions.ExtensionMetadata{},
		&internal.GlobalCommandOptions{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no extensions found")
}

func Test_SelectDistinctExtension_MultiMatch_NoPrompt(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Source: "source1"},
		{Source: "source2"},
	}
	_, err := selectDistinctExtension(
		t.Context(), mockinput.NewMockConsole(),
		"test-ext", exts,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple sources")
}

func Test_ConfigOptions_JsonFormatError(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newConfigOptionsAction(console, formatter, w, mgr, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed formatting config options")
}

// configSetAction.Run — Set error
// When a.b is attempted but a is a string, config.Set returns error.
func Test_ConfigSetAction_SetError(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"a": "scalar"})
	mgr := &finishConfigMgr{cfg: cfg}
	action := newConfigSetAction(mgr, []string{"a.b", "value"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed setting configuration")
}

// configUnsetAction.Run — Unset error
func Test_ConfigUnsetAction_UnsetError(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"a": "scalar"})
	mgr := &finishConfigMgr{cfg: cfg}
	action := newConfigUnsetAction(mgr, []string{"a.b"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed removing configuration")
}

// envSelectAction — console.Select error
func Test_EnvSelectAction_SelectError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(
		[]*environment.Description{{Name: "env1"}, {Name: "env2"}},
		nil,
	)

	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(_ input.ConsoleOptions) (any, error) {
		return 0, errors.New("select cancelled")
	})

	action := newEnvSelectAction(azdCtx, mgr, console, nil) // nil args → prompts
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "selecting environment")
}

// envSelectAction — SetProjectState error
func Test_EnvSelectAction_SetProjectStateError(t *testing.T) {
	t.Parallel()
	// Use a directory where .azure is a FILE instead of a directory,
	// so writing .azure/config.json fails.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, azdcontext.ProjectFileName), []byte("name: test\n"), 0600))
	// Create .azure as a regular file — SetProjectState will fail trying to write .azure/config.json
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".azure"), []byte("blocker"), 0600))
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

	env := environment.NewWithValues("env1", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	console := mockinput.NewMockConsole()
	action := newEnvSelectAction(azdCtx, mgr, console, []string{"env1"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "setting default environment")
}

func Test_NewInitFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInitFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInitCmd(t *testing.T) {
	t.Parallel()
	cmd := newInitCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "init")
}

func Test_ConfigSetAction_LoadError(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	action := newConfigSetAction(ucm, []string{"key", "value"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "load error")
}

func Test_ConfigUnsetAction_SaveError(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("mykey", "val")
	ucm := &pushConfigMgr{cfg: cfg, saveErr: errors.New("save error")}

	action := newConfigUnsetAction(ucm, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

func Test_ConfigUnsetAction_LoadError(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	action := newConfigUnsetAction(ucm, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "load error")
}

func Test_ConfigGetAction_NotFound(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	ucm := &pushConfigMgr{cfg: cfg}

	action := newConfigGetAction(ucm, &output.NoneFormatter{}, &bytes.Buffer{}, []string{"missing"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value at path")
}

func Test_PromptForExtensionChoice_Empty(t *testing.T) {
	t.Parallel()
	_, err := promptForExtensionChoice(t.Context(), mockinput.NewMockConsole(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no extensions")
}

func Test_EnvSetSecretAction_WithArgs_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(_ input.ConsoleOptions) (any, error) { return 0, fmt.Errorf("cancelled") })
	action := &envSetSecretAction{
		args:    []string{"MY_SECRET"},
		console: console,
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting secret setting strategy")
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	args := m.Called()
	return args.Get(0).(config.Config), args.Error(1)
}

func (m *mockUserConfigManager) Save(c config.Config) error {
	args := m.Called(c)
	return args.Error(0)
}

func Test_ExtensionSourceListAction_LoadError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), fmt.Errorf("config broken"))

	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        &bytes.Buffer{},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config broken")
}

func Test_PromptInitType_FromApp(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(0)

	result, err := promptInitType(console, t.Context(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, initType(initFromApp), result)
}

func Test_PromptInitType_Template(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1)

	result, err := promptInitType(console, t.Context(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, initType(initAppTemplate), result)
}

func Test_PromptInitType_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(_ input.ConsoleOptions) (any, error) { return 0, fmt.Errorf("cancelled") })

	_, err := promptInitType(console, t.Context(), nil, nil)
	require.Error(t, err)
}

func Test_SelectDistinctExtension_NoPrompt(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "a", DisplayName: "A", Source: "s1"},
		{Id: "b", DisplayName: "B", Source: "s2"},
	}
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{NoPrompt: true}
	_, err := selectDistinctExtension(t.Context(), console, "test.ext", exts, globalOpts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "found in multiple sources")
}
