// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/sethvargo/go-retry"
	"golang.org/x/exp/slices"
)

type PipelineAuthType string

const (
	AuthTypeFederated         PipelineAuthType = "federated"
	AuthTypeClientCredentials PipelineAuthType = "client-credentials"
)

var ErrAuthNotSupported = errors.New("pipeline authentication configuration is not supported")

type PipelineManagerArgs struct {
	PipelineServicePrincipalName string
	PipelineRemoteName           string
	PipelineRoleName             string
	PipelineProvider             string
	PipelineAuthTypeName         string
}

// PipelineManager takes care of setting up the scm and pipeline.
// The manager allows to use and test scm providers without a cobra command.
type PipelineManager struct {
	ScmProvider
	CiProvider
	AzdCtx      *azdcontext.AzdContext
	RootOptions *internal.GlobalCommandOptions
	Environment *environment.Environment
	PipelineManagerArgs
	azCli         azcli.AzCli
	commandRunner exec.CommandRunner
	console       input.Console
}

func NewPipelineManager(
	azCli azcli.AzCli,
	azdCtx *azdcontext.AzdContext,
	global *internal.GlobalCommandOptions,
	commandRunner exec.CommandRunner,
	console input.Console,
	args PipelineManagerArgs,
) *PipelineManager {
	return &PipelineManager{
		AzdCtx:              azdCtx,
		RootOptions:         global,
		PipelineManagerArgs: args,
		azCli:               azCli,
		commandRunner:       commandRunner,
		console:             console,
	}
}

// requiredTools get all the provider's required tools.
func (i *PipelineManager) requiredTools(ctx context.Context) []tools.ExternalTool {
	reqTools := i.ScmProvider.requiredTools(ctx)
	reqTools = append(reqTools, i.CiProvider.requiredTools(ctx)...)
	return reqTools
}

// preConfigureCheck invoke the validations from each provider.
func (i *PipelineManager) preConfigureCheck(ctx context.Context, infraOptions provisioning.Options) error {
	// Validate the authentication types
	// auth-type argument must either be an empty string or one of the following values.
	validAuthTypes := []string{string(AuthTypeFederated), string(AuthTypeClientCredentials)}
	pipelineAuthType := strings.TrimSpace(i.PipelineManagerArgs.PipelineAuthTypeName)
	if pipelineAuthType != "" && !slices.Contains(validAuthTypes, pipelineAuthType) {
		return fmt.Errorf(
			"pipeline authentication type '%s' is not valid. Valid authentication types are '%s'",
			i.PipelineManagerArgs.PipelineAuthTypeName,
			strings.Join(validAuthTypes, ", "),
		)
	}

	if err := i.CiProvider.preConfigureCheck(ctx, i.console, i.PipelineManagerArgs, infraOptions); err != nil {
		return fmt.Errorf("pre-config check error from %s provider: %w", i.CiProvider.name(), err)
	}
	if err := i.ScmProvider.preConfigureCheck(ctx, i.console, i.PipelineManagerArgs, infraOptions); err != nil {
		return fmt.Errorf("pre-config check error from %s provider: %w", i.ScmProvider.name(), err)
	}

	return nil
}

// ensureRemote get the git project details from a path and remote name using the scm provider.
func (i *PipelineManager) ensureRemote(
	ctx context.Context,
	repositoryPath string,
	remoteName string,
) (*gitRepositoryDetails, error) {
	gitCli := git.NewGitCli(i.commandRunner)
	remoteUrl, err := gitCli.GetRemoteUrl(ctx, repositoryPath, remoteName)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote url: %w", err)
	}

	// each provider knows how to extract the Owner and repo name from a remoteUrl
	gitRepoDetails, err := i.ScmProvider.gitRepoDetails(ctx, remoteUrl)
	if err != nil {
		return nil, err
	}
	gitRepoDetails.gitProjectPath = i.AzdCtx.ProjectDirectory()
	return gitRepoDetails, nil
}

// getGitRepoDetails get the details about a git project using the azd context to discover the project path.
func (i *PipelineManager) getGitRepoDetails(ctx context.Context) (*gitRepositoryDetails, error) {
	gitCli := git.NewGitCli(i.commandRunner)
	repoPath := i.AzdCtx.ProjectDirectory()
	for {
		repoRemoteDetails, err := i.ensureRemote(ctx, repoPath, i.PipelineRemoteName)
		switch {
		case errors.Is(err, git.ErrNotRepository):
			// Offer the user a chance to init a new repository if one does not exist.
			initRepo, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Initialize a new git repository?",
				DefaultValue: true,
			})
			if err != nil {
				return nil, fmt.Errorf("prompting for git init: %w", err)
			}

			if !initRepo {
				return nil, errors.New("confirmation declined")
			}

			if err := gitCli.InitRepo(ctx, repoPath); err != nil {
				return nil, fmt.Errorf("initializing repository: %w", err)
			}

			// Recovered from this error, try again
			continue
		case errors.Is(err, git.ErrNoSuchRemote):
			// Offer the user a chance to create the remote if one does not exist.
			addRemote, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf(
					"A remote named \"%s\" was not found. Would you like to configure one?",
					i.PipelineRemoteName,
				),
				DefaultValue: true,
			})
			if err != nil {
				return nil, fmt.Errorf("prompting for remote init: %w", err)
			}

			if !addRemote {
				return nil, errors.New("confirmation declined")
			}

			// the scm provider returns the repo url that is used as git remote
			remoteUrl, err := i.ScmProvider.configureGitRemote(ctx, repoPath, i.PipelineRemoteName, i.console)
			if err != nil {
				return nil, err
			}

			// set the git remote for local git project
			if err := gitCli.AddRemote(ctx, repoPath, i.PipelineRemoteName, remoteUrl); err != nil {
				return nil, fmt.Errorf("initializing repository: %w", err)
			}

			continue
		case err != nil:
			return nil, err
		default:
			return repoRemoteDetails, nil
		}
	}
}

// validateDependencyInjection panic if the manager did not received all the
// mandatory dependencies to work
func validateDependencyInjection(ctx context.Context, manager *PipelineManager) {
	if manager.ScmProvider == nil {
		log.Panic("missing scm provider for pipeline manager")
	}
	if manager.CiProvider == nil {
		log.Panic("missing CI provider for pipeline manager")
	}
}

// pushGitRepo commit all changes in the git project and push it to upstream.
func (i *PipelineManager) pushGitRepo(ctx context.Context, currentBranch string) error {
	gitCli := git.NewGitCli(i.commandRunner)

	if err := gitCli.AddFile(ctx, i.AzdCtx.ProjectDirectory(), "."); err != nil {
		return fmt.Errorf("adding files: %w", err)
	}

	if err := gitCli.Commit(ctx, i.AzdCtx.ProjectDirectory(), "Configure Azure Developer Pipeline"); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}

	i.console.Message(ctx, "Pushing changes")

	// If user has a git credential manager with some cached credentials
	// and the credentials are rotated, the push operation will fail and the credential manager would remove the cache
	// Then, on the next intent to push code, there should be a prompt for credentials.
	// Due to this, we use retry here, so we can run the second intent to prompt for credentials one more time
	return retry.Do(ctx, retry.WithMaxRetries(3, retry.NewConstant(100*time.Millisecond)), func(ctx context.Context) error {
		if err := gitCli.PushUpstream(ctx, i.AzdCtx.ProjectDirectory(), i.PipelineRemoteName, currentBranch); err != nil {
			return retry.RetryableError(fmt.Errorf("pushing changes: %w", err))
		}
		return nil
	})
}

// Configure is the main function from the pipeline manager which takes care
// of creating or setting up the git project, the ci pipeline and the Azure connection.
func (manager *PipelineManager) Configure(ctx context.Context) error {
	// check that scm and ci providers are set
	validateDependencyInjection(ctx, manager)

	// check all required tools are installed
	requiredTools := manager.requiredTools(ctx)
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	// Figure out what is the expected provider to use for provisioning
	prj, err := project.LoadProjectConfig(manager.AzdCtx.ProjectPath())
	if err != nil {
		return fmt.Errorf("finding provisioning provider: %w", err)
	}

	// run pre-config validations. manager will check az cli is logged in and
	// will invoke the per-provider validations.
	if errorsFromPreConfig := manager.preConfigureCheck(ctx, prj.Infra); errorsFromPreConfig != nil {
		return errorsFromPreConfig
	}

	// *********** Create or update Azure Principal ***********
	if manager.PipelineServicePrincipalName == "" {
		// This format matches what the `az` cli uses when a name is not provided, with the prefix
		// changed from "az-cli" to "az-dev"
		manager.PipelineServicePrincipalName = fmt.Sprintf("az-dev-%s", time.Now().UTC().Format("01-02-2006-15-04-05"))
	}

	manager.console.Message(
		ctx,
		fmt.Sprintf(
			"Creating or updating service principal %s.\n",
			output.WithHighLightFormat(manager.PipelineServicePrincipalName),
		),
	)

	credentials, err := manager.azCli.CreateOrUpdateServicePrincipal(
		ctx,
		manager.Environment.GetSubscriptionId(),
		manager.PipelineServicePrincipalName,
		manager.PipelineRoleName)
	if err != nil {
		return fmt.Errorf("failed to create or update service principal: %w", err)
	}

	// Get git repo details
	gitRepoInfo, err := manager.getGitRepoDetails(ctx)
	if err != nil {
		return fmt.Errorf("ensuring git remote: %w", err)
	}

	err = manager.CiProvider.configureConnection(
		ctx,
		manager.Environment,
		gitRepoInfo,
		prj.Infra,
		credentials,
		PipelineAuthType(manager.PipelineAuthTypeName),
		manager.console)
	if err != nil {
		return err
	}

	// config pipeline handles setting or creating the provider pipeline to be used
	err = manager.CiProvider.configurePipeline(ctx, gitRepoInfo, prj.Infra)
	if err != nil {
		return err
	}

	// The CI pipeline should be set-up and ready at this point.
	// azd offers to push changes to the scm to start a new pipeline run
	doPush, err := manager.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Would you like to commit and push your local changes to start the configured CI pipeline?",
		DefaultValue: true,
	})
	if err != nil {
		return fmt.Errorf("prompting to push: %w", err)
	}

	currentBranch, err := git.NewGitCli(manager.commandRunner).GetCurrentBranch(ctx, manager.AzdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}

	// scm provider can prevent from pushing changes and/or use the
	// interactive console for setting up any missing details.
	// For example, GitHub provider would check if GH-actions are disabled.
	if doPush {
		preventPush, err := manager.ScmProvider.preventGitPush(
			ctx,
			gitRepoInfo,
			manager.PipelineRemoteName,
			currentBranch,
			manager.console)
		if err != nil {
			return fmt.Errorf("check git push prevent: %w", err)
		}
		// revert user's choice when prevent git push returns true
		doPush = !preventPush
	}

	if doPush {
		err = manager.pushGitRepo(ctx, currentBranch)
		if err != nil {
			return fmt.Errorf("git push: %w", err)
		}

		gitRepoInfo.pushStatus = true
		err = manager.ScmProvider.postGitPush(
			ctx,
			gitRepoInfo,
			manager.PipelineRemoteName,
			currentBranch,
			manager.console)
		if err != nil {
			return fmt.Errorf("post git push hook: %w", err)
		}
	} else {
		manager.console.Message(ctx,
			fmt.Sprintf(
				"To fully enable pipeline you need to push this repo to the upstream using 'git push --set-upstream %s %s'.\n",
				manager.PipelineRemoteName,
				currentBranch))
	}

	return nil
}
