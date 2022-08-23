// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
)

// pipelineManager takes care of setting up the scm and pipeline.
// The manager allows to use and test scm providers without a cobra command.
type pipelineManager struct {
	scmProvider
	ciProvider
	console                      input.Console
	azdCtx                       *azdcontext.AzdContext
	rootOptions                  *commands.GlobalCommandOptions
	pipelineServicePrincipalName string
	pipelineRemoteName           string
	pipelineRoleName             string
}

func (i *pipelineManager) requiredTools() []tools.ExternalTool {
	reqTools := i.scmProvider.requiredTools()
	reqTools = append(reqTools, i.ciProvider.requiredTools()...)
	return reqTools
}

func (i *pipelineManager) preConfigureCheck(ctx context.Context) error {
	// make sure az is logged in
	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}
	if err := i.scmProvider.preConfigureCheck(ctx); err != nil {
		return fmt.Errorf("pre-config check error from %s provider: %w", i.scmProvider.name(), err)
	}
	if err := i.ciProvider.preConfigureCheck(ctx); err != nil {
		return fmt.Errorf("pre-config check error from %s provider: %w", i.ciProvider.name(), err)
	}

	return nil
}

func (i *pipelineManager) getSlugOrInit(ctx context.Context) (string, error) {
	gitCli := git.NewGitCli()

	for {
		repoSlug, err := github.EnsureRemote(ctx, i.azdCtx.ProjectDirectory(), i.pipelineRemoteName, gitCli)
		switch {
		case errors.Is(err, git.ErrNotRepository):
			// Offer the user a chance to init a new repository if one does not exist.
			initRepo, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Initialize a new git repository?",
				DefaultValue: true,
			})
			if err != nil {
				return repoSlug, fmt.Errorf("prompting for git init: %w", err)
			}

			if !initRepo {
				return repoSlug, errors.New("confirmation declined")
			}

			if err := gitCli.InitRepo(ctx, i.azdCtx.ProjectDirectory()); err != nil {
				return repoSlug, fmt.Errorf("initializing repository: %w", err)
			}

			// Recovered from this error, try again
			continue
		case errors.Is(err, git.ErrNoSuchRemote):
			// Offer the user a chance to create the remote if one does not exist.
			addRemote, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("A remote named \"%s\" was not found. Would you like to configure one?", i.pipelineRemoteName),
				DefaultValue: true,
			})
			if err != nil {
				return repoSlug, fmt.Errorf("prompting for remote init: %w", err)
			}

			if !addRemote {
				return repoSlug, errors.New("confirmation declined")
			}

			repoSlug, err = i.scmProvider.configureGitRemote(i.pipelineRemoteName)
			if err != nil {
				return repoSlug, err
			}
			// Recovered from this error, try again
			continue
		case err != nil:
			return repoSlug, err
		default:
			return repoSlug, nil
		}
	}
}

// validateDependencyInjection panic if the manager did not received all the
// mandatory dependencies to work
func validateDependencyInjection(manager *pipelineManager) {
	if manager.azdCtx == nil {
		log.Panic("missing azd context for pipeline manager")
	}
	if manager.scmProvider == nil {
		log.Panic("missing scm provider for pipeline manager")
	}
	if manager.ciProvider == nil {
		log.Panic("missing CI provider for pipeline manager")
	}
}

func (i *pipelineManager) pushGitRepo(ctx context.Context, currentBranch string) error {
	gitCli := git.NewGitCli()

	if err := gitCli.AddFile(ctx, i.azdCtx.ProjectDirectory(), "."); err != nil {
		return fmt.Errorf("adding files: %w", err)
	}

	if err := gitCli.Commit(ctx, i.azdCtx.ProjectDirectory(), "Configure GitHub Actions"); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}

	fmt.Println("Pushing changes")

	if err := gitCli.PushUpstream(ctx, i.azdCtx.ProjectDirectory(), i.pipelineRemoteName, currentBranch); err != nil {
		return fmt.Errorf("pushing changes: %w", err)
	}

	return nil
}

func (manager *pipelineManager) configure(ctx context.Context) error {

	// check that scm and ci providers are set
	validateDependencyInjection(manager)

	if err := ensureProject(manager.azdCtx.ProjectPath()); err != nil {
		return err
	}

	// check all required tools are installed
	azCli := commands.GetAzCliFromContext(ctx)
	requiredTools := manager.requiredTools()
	requiredTools = append(requiredTools, azCli)
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	if errorsFromPreConfig := manager.preConfigureCheck(ctx); errorsFromPreConfig != nil {
		return errorsFromPreConfig
	}

	// Read or init env
	env, err := loadOrInitEnvironment(ctx, &manager.rootOptions.EnvironmentName, manager.azdCtx, manager.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	// *********** Create or update Azure Principal ***********
	if manager.pipelineServicePrincipalName == "" {
		// This format matches what the `az` cli uses when a name is not provided, with the prefix
		// changed from "az-cli" to "az-dev"
		manager.pipelineServicePrincipalName = fmt.Sprintf("az-dev-%s", time.Now().UTC().Format("01-02-2006-15-04-05"))
	}
	fmt.Printf("Creating or updating service principal %s.\n", manager.pipelineServicePrincipalName)
	credentials, err := azCli.CreateOrUpdateServicePrincipal(
		ctx,
		env.GetSubscriptionId(),
		manager.pipelineServicePrincipalName,
		manager.pipelineRoleName)
	if err != nil {
		return fmt.Errorf("failed to create or update service principal: %w", err)
	}

	// Get owner and repo name
	repoSlug, err := manager.getSlugOrInit(ctx)
	if err != nil {
		return fmt.Errorf("ensuring github remote: %w", err)
	}

	// Config CI provider using credential
	err = manager.ciProvider.configureConnection(
		ctx,
		repoSlug,
		env.Values[environment.EnvNameEnvVarName],
		env.Values[environment.LocationEnvVarName],
		env.Values[environment.SubscriptionIdEnvVarName],
		credentials)
	if err != nil {
		return err
	}

	doPush, err := manager.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Would you like to commit and push your local changes to start the configured CI pipeline?",
		DefaultValue: true,
	})
	if err != nil {
		return fmt.Errorf("prompting to push: %w", err)
	}

	gitCli := git.NewGitCli()
	currentBranch, err := gitCli.GetCurrentBranch(ctx, manager.azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	if doPush {
		// scm provider can prevent from pushing changes after user choice
		preventPush, err := manager.scmProvider.preventGitPush(
			ctx,
			repoSlug,
			manager.pipelineRemoteName,
			currentBranch,
			manager.console)
		if err != nil {
			return fmt.Errorf("check git push prevent: %w", err)
		}
		// revert user's choice
		doPush = !preventPush
	}

	if doPush {
		err = manager.pushGitRepo(ctx, currentBranch)
		if err != nil {
			return err
		}
	} else {
		fmt.Printf("To fully enable GitHub Actions you need to push this repo to GitHub using 'git push --set-upstream %s %s'.\n", p.pipelineRemoteName, currentBranch)
	}

	return nil
}
