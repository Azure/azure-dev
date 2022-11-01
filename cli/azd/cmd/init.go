// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func initCmdDesign(rootOptions *internal.GlobalCommandOptions) (*cobra.Command, *initFlags) {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new application.",
		//nolint:lll
		Long: `Initialize a new application.

When no template is supplied, you can optionally select an Azure Developer CLI template for cloning. Otherwise, ` + output.WithBackticks("azd init") + ` initializes the current directory and creates resources so that your project is compatible with Azure Developer CLI.

When a template is provided, the sample code is cloned to the current directory.`,
	}

	f := &initFlags{}
	f.Bind(cmd.Flags(), rootOptions)

	return cmd, f
}

type initFlags struct {
	template       templates.Template
	templateBranch string
	subscription   string
	location       string
	rootOptions    *internal.GlobalCommandOptions
}

func (i *initFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(
		&i.template.Name,
		"template",
		"t",
		"",
		//nolint:lll
		"The template to use when you initialize the project. You can use Full URI, <owner>/<repository>, or <repository> if it's part of the azure-samples organization.",
	)
	local.StringVarP(&i.templateBranch, "branch", "b", "", "The template branch to initialize from.")
	local.StringVar(
		&i.subscription,
		"subscription",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")

	i.rootOptions = global
}

type initAction struct {
	azdCtx         *azdcontext.AzdContext
	accountManager *account.Manager
	console        input.Console
	cmdRun         exec.CommandRunner
	azCli          azcli.AzCli
	gitCli         git.GitCli
	flags          initFlags
}

func newInitAction(
	azdCtx *azdcontext.AzdContext,
	accountManager *account.Manager,
	cmdRun exec.CommandRunner,
	console input.Console,
	azCli azcli.AzCli,
	gitCli git.GitCli,
	flags initFlags) (*initAction, error) {
	return &initAction{
		azdCtx:         azdCtx,
		accountManager: accountManager,
		console:        console,
		cmdRun:         cmdRun,
		azCli:          azCli,
		gitCli:         gitCli,
		flags:          flags,
	}, nil
}

func (i *initAction) Run(ctx context.Context) error {
	// In the case where `init` is run and a parent folder already has an `azure.yaml` file, the
	// current ProjectDirectory will be set to that folder. That's not what we want here. We want
	// to force using the current working directory as a project root (since we are initializing a
	// new project).
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}

	log.Printf("forcing project directory to %s", wd)
	i.azdCtx.SetProjectDirectory(wd)

	if i.flags.templateBranch != "" && i.flags.template.Name == "" {
		return errors.New("template name required when specifying a branch name")
	}

	requiredTools := []tools.ExternalTool{i.azCli}

	// When using a template, we also require `git`, to acquire the template.
	if i.flags.template.Name != "" {
		requiredTools = append(requiredTools, i.gitCli)
	}

	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	// Project not initialized and no template specified
	if _, err := os.Stat(i.azdCtx.ProjectPath()); err != nil && errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(i.console.Handles().Stdout, "Initializing a new project in %s\n\n", wd)

		if i.flags.template.Name == "" {
			i.flags.template, err = templates.PromptTemplate(ctx, "Select a project template")

			if err != nil {
				return err
			}
		}
	}

	if i.flags.template.Name != "" {
		var templateUrl string

		if i.flags.template.RepositoryPath == "" {
			// using template name directly from command line
			i.flags.template.RepositoryPath = i.flags.template.Name
		}

		// treat names that start with http or git as full URLs and don't change them
		if strings.HasPrefix(i.flags.template.RepositoryPath, "git") ||
			strings.HasPrefix(i.flags.template.RepositoryPath, "http") {
			templateUrl = i.flags.template.RepositoryPath
		} else {
			switch strings.Count(i.flags.template.RepositoryPath, "/") {
			case 0:
				templateUrl = fmt.Sprintf("https://github.com/Azure-Samples/%s", i.flags.template.RepositoryPath)
			case 1:
				templateUrl = fmt.Sprintf("https://github.com/%s", i.flags.template.RepositoryPath)
			default:
				return fmt.Errorf("template '%s' should be either <repository> or <repo>/<repository>", i.flags.template.RepositoryPath)
			}
		}

		templateStagingDir, err := os.MkdirTemp("", "az-dev-template")
		if err != nil {
			return fmt.Errorf("creating temp folder: %w", err)
		}

		// Attempt to remove the temporary directory we cloned the template into, but don't fail the
		// overall operation if we can't.
		defer func() {
			_ = os.RemoveAll(templateStagingDir)
		}()

		spinner := spin.NewSpinner(i.console.Handles().Stdout, "Downloading template")
		err = spinner.Run(func() error {
			return i.gitCli.FetchCode(ctx, templateUrl, i.flags.templateBranch, templateStagingDir)
		})

		if err != nil {
			return fmt.Errorf("fetching template: %w", err)
		}

		log.Printf(
			"template init, checking for duplicates. source: %s target: %s",
			templateStagingDir,
			i.azdCtx.ProjectDirectory(),
		)

		// If there are any existing files in the destination that would be overwritten by files from the
		// template, have the user confirm they would like to overwrite these files. This is a more relaxed
		// check than just failing the init operation when a template is provided if there are any files
		// present (a scenario we'd like to support for cases where someone may say initialize a git repository
		// in the target directory or create a virtual env before running init).
		var duplicateFiles []string
		if err := filepath.WalkDir(templateStagingDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if d.IsDir() {
				return nil
			}

			partial, err := filepath.Rel(templateStagingDir, path)
			if err != nil {
				return fmt.Errorf("computing relative path: %w", err)
			}

			if _, err := os.Stat(filepath.Join(i.azdCtx.ProjectDirectory(), partial)); err == nil {
				duplicateFiles = append(duplicateFiles, partial)
			}

			return nil
		}); err != nil {
			return fmt.Errorf("enumerating template files: %w", err)
		}

		if len(duplicateFiles) > 0 {
			fmt.Fprintf(
				i.console.Handles().Stdout,
				"warning: the following files will be overwritten with the versions from the template: \n")
			for _, file := range duplicateFiles {
				fmt.Fprintf(i.console.Handles().Stdout, " * %s\n", file)
			}

			overwrite, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Overwrite files with versions from template?",
				DefaultValue: false,
			})

			if err != nil {
				return fmt.Errorf("prompting to overwrite: %w", err)
			}

			if !overwrite {
				return errors.New("confirmation declined")
			}
		}

		if err := copy.Copy(templateStagingDir, i.azdCtx.ProjectDirectory()); err != nil {
			return fmt.Errorf("copying template contents: %w", err)
		}
	}

	envName, err := i.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return environment.NewEnvironmentInitError(envName)
	}

	// Check to see if `azure.yaml` exists, and if it doesn't, create it.
	_, err = os.Stat(i.azdCtx.ProjectPath())

	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(i.console.Handles().Stdout, "Creating a new %s file.\n", azdcontext.ProjectFileName)

		_, err = project.NewProject(i.azdCtx.ProjectPath(), i.azdCtx.GetDefaultProjectName())

		if err != nil {
			return fmt.Errorf("failed to create a project file: %w", err)
		}
	}

	//create .azure when running azd init
	err = os.MkdirAll(
		filepath.Join(i.azdCtx.ProjectDirectory(), azdcontext.EnvironmentDirectoryName),
		osutil.PermissionDirectory,
	)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	//create .gitignore or open existing .gitignore file, and contains .azure
	gitignoreFile, err := os.OpenFile(
		filepath.Join(i.azdCtx.ProjectDirectory(), ".gitignore"),
		os.O_APPEND|os.O_RDWR|os.O_CREATE,
		osutil.PermissionFile,
	)
	if err != nil {
		return fmt.Errorf("fail to create or open .gitignore: %w", err)
	}
	defer gitignoreFile.Close()

	writeGitignoreFile := true
	//bufio scanner splits on new lines by default
	scanner := bufio.NewScanner(gitignoreFile)
	for scanner.Scan() {
		if azdcontext.EnvironmentDirectoryName == scanner.Text() {
			writeGitignoreFile = false
		}
	}

	if writeGitignoreFile {
		newLine := osutil.GetNewLineSeparator()
		_, err := gitignoreFile.WriteString(newLine + azdcontext.EnvironmentDirectoryName + newLine)
		if err != nil {
			return fmt.Errorf("fail to write '%s' in .gitignore: %w", azdcontext.EnvironmentDirectoryName, err)
		}
	}

	envSpec := environmentSpec{
		environmentName: i.flags.rootOptions.EnvironmentName,
		subscription:    i.flags.subscription,
		location:        i.flags.location,
	}
	env, ctx, err := createAndInitEnvironment(ctx, &envSpec, i.azdCtx, i.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	if err := i.azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
		return fmt.Errorf("saving default environment: %w", err)
	}

	// If the configuration is empty, set default subscription & location
	// This will be the case for first run experience
	if !i.accountManager.HasDefaults() {
		_, err = i.accountManager.SetDefaultSubscription(ctx, env.GetSubscriptionId())
		if err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
		_, err = i.accountManager.SetDefaultLocation(ctx, env.GetSubscriptionId(), env.GetLocation())
		if err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	return nil
}
