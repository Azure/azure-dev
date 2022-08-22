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

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func initCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&initAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"init",
		"Initialize a new application.",
		`Initialize a new application.

When no template is supplied, you can optionally select an Azure Developer CLI template for cloning. Otherwise, `+withBackticks("azd init")+` initializes the current directory and creates resources so that your project is compatible with Azure Developer CLI.

When a template is provided, the sample code is cloned to the current directory.`,
	)
	return cmd
}

type initAction struct {
	template       templates.Template
	templateBranch string
	subscription   string
	location       string
	rootOptions    *commands.GlobalCommandOptions
}

func (i *initAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.StringVarP(&i.template.Name, "template", "t", "", "The template to use when you initialize the project. You can use Full URI, <owner>/<repository>, or <repository> if it's part of the azure-samples organization.")
	local.StringVarP(&i.templateBranch, "branch", "b", "", "The template branch to initialize from.")
	local.StringVar(&i.subscription, "subscription", "", "Name or ID of an Azure subscription to use for the new environment")
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
}

func (i *initAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	// In the case where `init` is run and a parent folder already has an `azure.yaml` file, the
	// current ProjectDirectory will be set to that folder. That's not what we want here. We want
	// to force using the current working directory as a project root (since we are initializing a
	// new project).
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}

	log.Printf("forcing project directory to %s", wd)
	azdCtx.SetProjectDirectory(wd)

	if i.templateBranch != "" && i.template.Name == "" {
		return errors.New("template name required when specifying a branch name")
	}

	console := input.NewConsole(!i.rootOptions.NoPrompt)
	azCli := commands.GetAzCliFromContext(ctx)
	gitCli := git.NewGitCli()

	requiredTools := []tools.ExternalTool{azCli}

	// When using a template, we also require `git`, to acquire the template.
	if i.template.Name != "" {
		requiredTools = append(requiredTools, gitCli)
	}

	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	// Project not initialized and no template specified
	if _, err := os.Stat(azdCtx.ProjectPath()); err != nil && errors.Is(err, os.ErrNotExist) {
		fmt.Printf("Initializing a new project in %s\n\n", wd)

		if i.template.Name == "" {
			i.template, err = console.PromptTemplate(ctx, "Select a project template")

			if err != nil {
				return err
			}
		}
	}

	if i.template.Name != "" {
		var templateUrl string

		if i.template.RepositoryPath == "" {
			// using template name directly from command line
			i.template.RepositoryPath = i.template.Name
		}

		// treat names that start with http or git as full URLs and don't change them
		if strings.HasPrefix(i.template.RepositoryPath, "git") || strings.HasPrefix(i.template.RepositoryPath, "http") {
			templateUrl = i.template.RepositoryPath
		} else {
			switch strings.Count(i.template.RepositoryPath, "/") {
			case 0:
				templateUrl = fmt.Sprintf("https://github.com/Azure-Samples/%s", i.template.RepositoryPath)
			case 1:
				templateUrl = fmt.Sprintf("https://github.com/%s", i.template.RepositoryPath)
			default:
				return fmt.Errorf("template '%s' should be either <repository> or <repo>/<repository>", i.template.RepositoryPath)
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

		initFunc := func() error {
			return gitCli.FetchCode(ctx, templateUrl, i.templateBranch, templateStagingDir)
		}

		spinner := spin.NewSpinner("Downloading template")
		if err := spinner.Run(
			initFunc,
		); err != nil {
			return fmt.Errorf("fetching template: %w", err)
		}

		log.Printf("template init, checking for duplicates. source: %s target: %s", templateStagingDir, azdCtx.ProjectDirectory())

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

			if _, err := os.Stat(filepath.Join(azdCtx.ProjectDirectory(), partial)); err == nil {
				duplicateFiles = append(duplicateFiles, partial)
			}

			return nil
		}); err != nil {
			return fmt.Errorf("enumerating template files: %w", err)
		}

		if len(duplicateFiles) > 0 {
			fmt.Printf("warning: the following files will be overwritten with the versions from the template: \n")
			for _, file := range duplicateFiles {
				fmt.Printf(" * %s\n", file)
			}

			overwrite, err := console.Confirm(ctx, input.ConsoleOptions{
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

		if err := copy.Copy(templateStagingDir, azdCtx.ProjectDirectory()); err != nil {
			return fmt.Errorf("copying template contents: %w", err)
		}
	}

	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return environment.NewEnvironmentInitError(envName)
	}

	_, err = project.LoadProjectConfig(azdCtx.ProjectPath(), &environment.Environment{})

	if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("Creating a new %s file.\n", azdcontext.ProjectFileName)

		_, err = project.NewProject(azdCtx.ProjectPath(), azdCtx.GetDefaultProjectName())

		if err != nil {
			return fmt.Errorf("failed to create a project file: %w", err)
		}
	}

	//create .azure when running azd init
	err = os.MkdirAll(filepath.Join(azdCtx.ProjectDirectory(), azdcontext.EnvironmentDirectoryName), osutil.PermissionDirectory)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	//create .gitignore or open existing .gitignore file, and contains .azure
	gitignoreFile, err := os.OpenFile(filepath.Join(azdCtx.ProjectDirectory(), ".gitignore"), os.O_APPEND|os.O_RDWR|os.O_CREATE, osutil.PermissionFile)
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
		environmentName: i.rootOptions.EnvironmentName,
		subscription:    i.subscription,
		location:        i.location,
	}
	_, err = createAndInitEnvironment(ctx, &envSpec, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
		return fmt.Errorf("saving default environment: %w", err)
	}

	return nil
}

const (
	// CodespacesEnvVarName is the name of the env variable set when you're in a Github codespace. It's
	// just set to 'true'.
	CodespacesEnvVarName = "CODESPACES"

	// RemoteContainersEnvVarName is the name of the env variable set when you're in a remote container. It's
	// just set to 'true'.
	RemoteContainersEnvVarName = "REMOTE_CONTAINERS"
)
