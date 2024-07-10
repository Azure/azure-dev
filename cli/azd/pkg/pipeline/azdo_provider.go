// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	azdoGit "github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

// AzdoScmProvider implements ScmProvider using Azure DevOps as the provider
// for source control manager.
type AzdoScmProvider struct {
	envManager     environment.Manager
	repoDetails    *AzdoRepositoryDetails
	env            *environment.Environment
	azdContext     *azdcontext.AzdContext
	azdoConnection *azuredevops.Connection
	commandRunner  exec.CommandRunner
	console        input.Console
	gitCli         git.GitCli
}

func NewAzdoScmProvider(
	envManager environment.Manager,
	env *environment.Environment,
	azdContext *azdcontext.AzdContext,
	commandRunner exec.CommandRunner,
	console input.Console,
	gitCli git.GitCli,
) ScmProvider {
	return &AzdoScmProvider{
		envManager:    envManager,
		env:           env,
		azdContext:    azdContext,
		commandRunner: commandRunner,
		console:       console,
		gitCli:        gitCli,
	}
}

// AzdoRepositoryDetails provides extra state needed for the AzDo provider.
// this is stored as the details property in repoDetails
type AzdoRepositoryDetails struct {
	projectName     string
	projectId       string
	repoId          string
	orgName         string
	repoName        string
	repoWebUrl      string
	remoteUrl       string
	sshUrl          string
	buildDefinition *build.BuildDefinition
}

// ***  subareaProvider implementation ******

// requiredTools return the list of external tools required by
// Azure DevOps provider during its execution.
func (p *AzdoScmProvider) requiredTools(_ context.Context) ([]tools.ExternalTool, error) {
	return []tools.ExternalTool{}, nil
}

// preConfigureCheck check the current state of external tools and any
// other dependency to be as expected for execution.
func (p *AzdoScmProvider) preConfigureCheck(
	ctx context.Context,
	pipelineManagerArgs PipelineManagerArgs,
	infraOptions provisioning.Options,
	projectPath string,
) (bool, error) {
	_, updatedPat, err := azdo.EnsurePatExists(ctx, p.env, p.console)
	if err != nil {
		return updatedPat, err
	}

	_, updatedOrg, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.env, p.console)
	return (updatedPat || updatedOrg), err
}

// helper function to save configuration values to .env file
func (p *AzdoScmProvider) saveEnvironmentConfig(ctx context.Context, key string, value string) error {
	p.env.DotenvSet(key, value)
	err := p.envManager.Save(ctx, p.env)
	if err != nil {
		return err
	}
	return nil
}

// name returns the name of the provider
func (p *AzdoScmProvider) Name() string {
	return azdoDisplayName
}

// ***  scmProvider implementation ******

// stores repo details in state for use in other functions. Also saves AzDo project details to .env
func (p *AzdoScmProvider) StoreRepoDetails(ctx context.Context, repo *azdoGit.GitRepository) error {
	repoDetails := p.getRepoDetails()
	repoDetails.repoName = *repo.Name
	repoDetails.remoteUrl = *repo.RemoteUrl
	repoDetails.repoWebUrl = *repo.WebUrl
	repoDetails.sshUrl = *repo.SshUrl
	repoDetails.repoId = repo.Id.String()

	err := p.saveEnvironmentConfig(ctx, azdo.AzDoEnvironmentRepoIdName, p.repoDetails.repoId)
	if err != nil {
		return fmt.Errorf("error saving repo id to environment %w", err)
	}

	err = p.saveEnvironmentConfig(ctx, azdo.AzDoEnvironmentRepoName, p.repoDetails.repoName)
	if err != nil {
		return fmt.Errorf("error saving repo name to environment %w", err)
	}

	err = p.saveEnvironmentConfig(ctx, azdo.AzDoEnvironmentRepoWebUrl, p.repoDetails.repoWebUrl)
	if err != nil {
		return fmt.Errorf("error saving repo web url to environment %w", err)
	}

	return nil
}

// prompts the user for a new AzDo Git repo and creates the repo
func (p *AzdoScmProvider) createNewGitRepositoryFromInput(ctx context.Context, console input.Console) (string, error) {
	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	var repo *azdoGit.GitRepository
	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new Azure DevOps Repository OR Hit enter to use this name:",
			DefaultValue: p.repoDetails.projectName,
		})
		if err != nil {
			return "", fmt.Errorf("asking for new project name: %w", err)
		}

		var message string
		newRepo, err := azdo.CreateRepository(ctx, p.repoDetails.projectId, name, connection)
		if err != nil {
			message = err.Error()
		}
		if strings.Contains(message, fmt.Sprintf("A Git repository with the name %s already exists.", name)) {
			console.Message(ctx, fmt.Sprintf("error: the repo name '%s' is already in use\n", name))
			continue // try again
		} else if strings.Contains(message, "TF401025: 'repoName' is not a valid name for a Git repository.") {
			console.Message(ctx, fmt.Sprintf(
				"error: '%s' is not a valid Azure DevOps repo name. "+
					"See https://aka.ms/azure-dev/azdo-repo-naming\n", name))
			continue // try again
		} else if err != nil {
			return "", fmt.Errorf("creating repository: %w", err)
		} else {
			repo = newRepo
			break
		}
	}

	err = p.StoreRepoDetails(ctx, repo)
	if err != nil {
		return "", err

	}
	return *repo.RemoteUrl, nil
}

// verifies that a repo exists or prompts the user to select from a list of existing AzDo repos
func (p *AzdoScmProvider) ensureGitRepositoryExists(ctx context.Context, console input.Console) (string, error) {
	if p.repoDetails != nil && p.repoDetails.repoName != "" {
		return p.repoDetails.remoteUrl, nil
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	repo, err := azdo.GetGitRepositoriesInProject(ctx, p.repoDetails.projectName, p.repoDetails.orgName, connection, console)
	if err != nil {
		return "", err
	}

	err = p.StoreRepoDetails(ctx, repo)
	if err != nil {
		return "", err
	}

	return *repo.RemoteUrl, nil
}

// helper function to return repoDetails from state
func (p *AzdoScmProvider) getRepoDetails() *AzdoRepositoryDetails {
	if p.repoDetails != nil {
		return p.repoDetails
	}
	repoDetails := &AzdoRepositoryDetails{}
	p.repoDetails = repoDetails
	return p.repoDetails
}

// helper function to return an azuredevops.Connection for use with AzDo Go SDK
func (p *AzdoScmProvider) getAzdoConnection(ctx context.Context) (*azuredevops.Connection, error) {
	if p.azdoConnection != nil {
		return p.azdoConnection, nil
	}

	org, _, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.env, p.console)
	if err != nil {
		return nil, err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.orgName = org

	pat, _, err := azdo.EnsurePatExists(ctx, p.env, p.console)
	if err != nil {
		return nil, err
	}

	connection, err := azdo.GetConnection(ctx, org, pat)
	if err != nil {
		return nil, err
	}

	return connection, nil
}

// returns an existing project or prompts the user to either select a project or a create a new AzDo project
func (p *AzdoScmProvider) ensureProjectExists(ctx context.Context, console input.Console) (string, string, bool, error) {
	if p.repoDetails != nil && p.repoDetails.projectName != "" {
		return p.repoDetails.projectName, p.repoDetails.projectId, false, nil
	}
	idx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How would you like to configure your git remote to Azure DevOps?",
		Options: []string{
			"Select an existing Azure DevOps project",
			"Create a new Azure DevOps Project",
		},
		DefaultValue: "Create a new Azure DevOps Project",
	})
	if err != nil {
		return "", "", false, fmt.Errorf("prompting for azdo project type: %w", err)
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", "", false, err
	}

	var projectName string
	var projectId string
	var newProject bool = false
	switch idx {
	// Select from an existing AzDo project
	case 0:
		projectName, projectId, err = azdo.GetProjectFromExisting(ctx, connection, console)
		if err != nil {
			return "", "", false, err
		}
	// Create a new AzDo project
	case 1:
		projectName, projectId, err = azdo.GetProjectFromNew(
			ctx,
			p.azdContext.ProjectDirectory(),
			connection,
			p.env,
			console,
		)
		newProject = true
		if err != nil {
			return "", "", false, err
		}
	default:
		panic(fmt.Sprintf("unexpected selection index %d", idx))
	}
	return projectName, projectId, newProject, nil
}

// configureGitRemote set up or create the git project and git remote
func (p *AzdoScmProvider) configureGitRemote(
	ctx context.Context,
	repoPath string,
	remoteName string,
) (string, error) {
	projectName, projectId, newProject, err := p.ensureProjectExists(ctx, p.console)
	if err != nil {
		return "", err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.projectName = projectName
	repoDetails.projectId = projectId

	err = p.saveEnvironmentConfig(ctx, azdo.AzDoEnvironmentProjectIdName, projectId)
	if err != nil {
		return "", fmt.Errorf("error saving project id to environment %w", err)
	}

	err = p.saveEnvironmentConfig(ctx, azdo.AzDoEnvironmentProjectName, projectName)
	if err != nil {
		return "", fmt.Errorf("error saving project name to environment %w", err)
	}
	var remoteUrl string

	if !newProject {
		remoteUrl, err = p.promptForAzdoRepository(ctx, p.console)
		if err != nil {
			return "", err
		}
	} else {
		remoteUrl, err = p.getDefaultRepoRemote(ctx, projectName, projectId, p.console)
		if err != nil {
			return "", err
		}
	}

	branch, err := p.getCurrentGitBranch(ctx, repoPath)
	if err != nil {
		return "", err
	}
	azdo.DefaultBranch = branch

	return remoteUrl, nil
}

func (p *AzdoScmProvider) getCurrentGitBranch(ctx context.Context, repoPath string) (string, error) {
	branch, err := p.gitCli.GetCurrentBranch(ctx, repoPath)
	if err != nil {
		return "", err
	}
	return branch, nil
}

// returns the git remote for a newly created repo that is part of a newly created AzDo project
func (p *AzdoScmProvider) getDefaultRepoRemote(
	ctx context.Context,
	projectName string,
	projectId string,
	console input.Console,
) (string, error) {
	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}
	repo, err := azdo.GetDefaultGitRepositoriesInProject(ctx, projectName, connection)
	if err != nil {
		return "", err
	}

	err = p.StoreRepoDetails(ctx, repo)
	if err != nil {
		return "", err
	}

	return *repo.RemoteUrl, nil
}

// prompt the user to select azdo repo or create a new one
func (p *AzdoScmProvider) promptForAzdoRepository(ctx context.Context, console input.Console) (string, error) {
	var remoteUrl string
	// There are a few ways to configure the remote so offer a choice to the user.
	idx, err := console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("How would you like to configure your remote? (Organization: %s)", p.repoDetails.projectName),
		Options: []string{
			"Select an existing Azure DevOps Repository",
			"Create a new private Azure DevOps Repository",
		},
		DefaultValue: "Create a new private Azure DevOps Repository",
	})

	if err != nil {
		return "", fmt.Errorf("prompting for remote configuration type: %w", err)
	}

	switch idx {
	// Select from an existing Azure DevOps project
	case 0:
		remoteUrl, err = p.ensureGitRepositoryExists(ctx, console)
		if err != nil {
			return "", err
		}

	// Create a new project
	case 1:
		remoteUrl, err = p.createNewGitRepositoryFromInput(ctx, console)
		if err != nil {
			return "", err
		}

	default:
		panic(fmt.Sprintf("unexpected selection index %d", idx))
	}

	return remoteUrl, nil
}

// defines the structure of an ssl git remote
var azdoRemoteGitUrlRegex = regexp.MustCompile(`^git@ssh.dev.azure\.com:(.*?)(?:\.git)?$`)

// defines the structure of an HTTPS git remote
var azdoRemoteHttpsUrlRegex = regexp.MustCompile(`^https://[a-zA-Z0-9]+(?:-[a-zA-Z0-9]+)*:*.+@dev.azure\.com/(.*?)$`)

// ErrRemoteHostIsNotAzDo the error used when a non Azure DevOps remote is found
var ErrRemoteHostIsNotAzDo = errors.New("existing remote is not an Azure DevOps host")

// ErrSSHNotSupported the error used when ssh git remote is detected
var ErrSSHNotSupported = errors.New("ssh git remote is not supported. " +
	"Use HTTPS git remote to connect the remote repository")

// helper function to determine if the provided remoteUrl is an azure devops repo.
// currently supports AzDo PaaS
func isAzDoRemote(remoteUrl string) error {
	if azdoRemoteGitUrlRegex.MatchString(remoteUrl) {
		return ErrSSHNotSupported
	}
	slug := ""
	for _, r := range []*regexp.Regexp{azdoRemoteGitUrlRegex, azdoRemoteHttpsUrlRegex} {
		captures := r.FindStringSubmatch(remoteUrl)
		if captures != nil {
			slug = captures[1]
		}
	}
	if slug == "" {
		return ErrRemoteHostIsNotAzDo
	}
	return nil
}

func parseAzDoRemote(remoteUrl string) (string, error) {
	for _, r := range []*regexp.Regexp{azdoRemoteGitUrlRegex, azdoRemoteHttpsUrlRegex} {
		captures := r.FindStringSubmatch(remoteUrl)
		if captures != nil {
			return captures[1], nil
		}
	}
	return "", nil
}

// gitRepoDetails extracts the information from an Azure DevOps remote url into general scm concepts
// like owner, name and path
func (p *AzdoScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	err := isAzDoRemote(remoteUrl)
	if err != nil {
		return nil, err
	}

	repoDetails := p.getRepoDetails()
	// Try getting values from the env.
	// This is a quick shortcut to avoid parsing the remote in detail.
	// While using the same .env file, the outputs from creating a project and repository
	// are memorized in .env file
	if repoDetails.orgName == "" {
		repoDetails.orgName = p.env.Getenv(azdo.AzDoEnvironmentOrgName)
	}
	if repoDetails.projectName == "" {
		repoDetails.projectName = p.env.Getenv(azdo.AzDoEnvironmentProjectName)
	}
	if repoDetails.projectId == "" {
		repoDetails.projectId = p.env.Getenv(azdo.AzDoEnvironmentProjectIdName)
	}
	if repoDetails.repoName == "" {
		repoDetails.repoName = p.env.Getenv(azdo.AzDoEnvironmentRepoName)
	}
	if repoDetails.repoId == "" {
		repoDetails.repoId = p.env.Getenv(azdo.AzDoEnvironmentRepoIdName)
	}
	if repoDetails.repoWebUrl == "" {
		repoDetails.repoWebUrl = p.env.Getenv(azdo.AzDoEnvironmentRepoWebUrl)
	}
	if repoDetails.remoteUrl == "" {
		repoDetails.remoteUrl = remoteUrl
	}

	if repoDetails.projectId == "" || repoDetails.repoId == "" {
		// Removing environment or creating a new one would remove any memory fro project
		// and repo.  In that case, it needs to be calculated from the remote url
		azdoSlug, err := parseAzDoRemote(remoteUrl)
		if err != nil {
			return nil, fmt.Errorf("parsing Azure DevOps remote url: %s: %w", remoteUrl, err)
		}
		// azdoSlug => Org/Project/_git/repoName
		parts := strings.Split(azdoSlug, "_git/")
		repoDetails.projectName = strings.Split(parts[0], "/")[1]
		p.env.DotenvSet(azdo.AzDoEnvironmentProjectName, repoDetails.projectName)
		repoDetails.repoName = parts[1]
		p.env.DotenvSet(azdo.AzDoEnvironmentRepoName, repoDetails.repoName)

		connection, err := p.getAzdoConnection(ctx)
		if err != nil {
			return nil, fmt.Errorf("Getting azdo connection: %w", err)
		}

		repo, err := azdo.GetGitRepository(ctx, repoDetails.projectName, repoDetails.repoName, connection)
		if err != nil {
			return nil, fmt.Errorf("Looking for repository: %w", err)
		}
		repoDetails.repoId = repo.Id.String()
		p.env.DotenvSet(azdo.AzDoEnvironmentRepoIdName, repoDetails.repoId)
		repoDetails.repoWebUrl = *repo.WebUrl
		p.env.DotenvSet(azdo.AzDoEnvironmentRepoWebUrl, repoDetails.repoWebUrl)

		proj, err := azdo.GetProjectByName(ctx, connection, repoDetails.projectName)
		if err != nil {
			return nil, fmt.Errorf("Looking for project: %w", err)
		}
		repoDetails.projectId = proj.Id.String()
		p.env.DotenvSet(azdo.AzDoEnvironmentProjectIdName, repoDetails.projectId)

		if err := p.envManager.Save(ctx, p.env); err != nil {
			return nil, fmt.Errorf("saving environment: %w", err)
		}
	}

	return &gitRepositoryDetails{
		owner:    p.repoDetails.orgName,
		repoName: p.repoDetails.repoName,
		details:  repoDetails,
		remote:   repoDetails.remoteUrl,
		url:      repoDetails.repoWebUrl,
	}, nil
}

// preventGitPush is nil for Azure DevOps
func (p *AzdoScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string) (bool, error) {
	return false, nil
}

func azdoPat(ctx context.Context, env *environment.Environment, console input.Console) string {
	pat, _, err := azdo.EnsurePatExists(ctx, env, console)
	if err != nil {
		log.Printf("Error getting PAT when it should be found: %s", err.Error())
	}
	return pat
}

func gitInsteadOfConfig(
	pat string,
	gitRepo *gitRepositoryDetails) (string, string) {

	azdoRepoDetails := gitRepo.details.(*AzdoRepositoryDetails)
	remoteAndPatUrl := fmt.Sprintf("url.https://%s@%s/", pat, azdo.AzDoHostName)
	originalUrl := fmt.Sprintf("https://%s@%s/", azdoRepoDetails.orgName, azdo.AzDoHostName)
	return remoteAndPatUrl, originalUrl
}

// Push code and queue pipeline
func (p *AzdoScmProvider) GitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string) error {

	// ** Push code with PAT
	// This is the same as gitCli.PushUpstream(), but it adds `-c url.PAT+HostName.insteadOf=HostName` to execute
	// git push with the PAT to authenticate
	pat := azdoPat(ctx, p.env, p.console)
	remoteAndPatUrl, originalUrl := gitInsteadOfConfig(pat, gitRepo)
	runArgs := exec.NewRunArgsWithSensitiveData("git",
		[]string{
			"-C",
			gitRepo.gitProjectPath,
			"-c",
			fmt.Sprintf("%s.insteadOf=%s", remoteAndPatUrl, originalUrl),
			"push",
			"--set-upstream",
			"--quiet",
			remoteName,
			branchName,
		},
		[]string{
			pat,
		},
	).WithInteractive(true)
	if _, err := p.commandRunner.Run(ctx, runArgs); err != nil {
		// this error should not fail the operation
		log.Printf("Error setting git config: insteadOf url: %s", err.Error())
	}

	// *** Queue pipeline
	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return err
	}
	err = azdo.CreateBuildPolicy(
		ctx,
		connection,
		p.repoDetails.projectId,
		p.repoDetails.repoId,
		p.repoDetails.buildDefinition,
		p.env,
	)
	if err != nil {
		return err
	}

	err = azdo.QueueBuild(
		ctx, connection, p.repoDetails.projectId, p.repoDetails.buildDefinition, branchName)
	if err != nil {
		return err
	}

	return nil
}

// AzdoCiProvider implements a CiProvider using Azure DevOps to manage CI with azdo pipelines.
type AzdoCiProvider struct {
	envManager    environment.Manager
	Env           *environment.Environment
	AzdContext    *azdcontext.AzdContext
	credentials   *entraid.AzureCredentials
	console       input.Console
	commandRunner exec.CommandRunner
}

func NewAzdoCiProvider(
	envManager environment.Manager,
	env *environment.Environment,
	azdContext *azdcontext.AzdContext,
	console input.Console,
	commandRunner exec.CommandRunner,
) CiProvider {
	return &AzdoCiProvider{
		envManager:    envManager,
		Env:           env,
		AzdContext:    azdContext,
		console:       console,
		commandRunner: commandRunner,
	}
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *AzdoCiProvider) requiredTools(_ context.Context) ([]tools.ExternalTool, error) {
	return []tools.ExternalTool{}, nil
}

// preConfigureCheck nil for Azdo
func (p *AzdoCiProvider) preConfigureCheck(
	ctx context.Context,
	pipelineManagerArgs PipelineManagerArgs,
	infraOptions provisioning.Options,
	projectPath string,
) (bool, error) {
	authType := PipelineAuthType(pipelineManagerArgs.PipelineAuthTypeName)

	if authType == AuthTypeFederated {
		return false, fmt.Errorf(
			//nolint:lll
			"Azure DevOps does not support federated authentication. To explicitly use client credentials set the %s flag. %w",
			output.WithBackticks("--auth-type client-credentials"),
			ErrAuthNotSupported,
		)
	}

	_, updatedPat, err := azdo.EnsurePatExists(ctx, p.Env, p.console)
	if err != nil {
		return updatedPat, err
	}

	_, updatedOrg, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.Env, p.console)
	return (updatedPat || updatedOrg), err
}

// name returns the name of the provider.
func (p *AzdoCiProvider) Name() string {
	return azdoDisplayName
}

// ***  ciProvider implementation ******

func (p *AzdoCiProvider) credentialOptions(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	infraOptions provisioning.Options,
	authType PipelineAuthType,
	credentials *entraid.AzureCredentials,
) (*CredentialOptions, error) {
	// Default auth type to client-credentials for terraform
	if infraOptions.Provider == provisioning.Terraform && authType == "" {
		authType = AuthTypeClientCredentials
	}

	if authType == AuthTypeClientCredentials {
		return &CredentialOptions{
			EnableClientCredentials: true,
		}, nil
	}

	// If not specified default to federated credentials
	if authType == "" || authType == AuthTypeFederated {
		p.credentials = credentials
		details := repoDetails.details.(*AzdoRepositoryDetails)
		org, _, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.Env, p.console)
		if err != nil {
			return nil, err
		}
		pat, _, err := azdo.EnsurePatExists(ctx, p.Env, p.console)
		if err != nil {
			return nil, err
		}
		connection, err := azdo.GetConnection(ctx, org, pat)
		if err != nil {
			return nil, err
		}
		sConnection, err := azdo.CreateServiceConnection(
			ctx, connection, details.projectId, details.projectName, *p.Env, p.credentials, p.console)
		if err != nil {
			return nil, err
		}
		federatedCredentials := []*graphsdk.FederatedIdentityCredential{
			{
				Name:        "AzureDevOpsOIDC", //Must not contain a space character and 3 to 64 characters in length
				Issuer:      (*sConnection.Authorization.Parameters)["workloadIdentityFederationIssuer"],
				Subject:     (*sConnection.Authorization.Parameters)["workloadIdentityFederationSubject"],
				Description: convert.RefOf("Created by Azure Developer CLI"),
				Audiences:   []string{federatedIdentityAudience},
			},
		}
		return &CredentialOptions{
			EnableFederatedCredentials: true,
			FederatedCredentialOptions: federatedCredentials,
		}, nil
	}

	return &CredentialOptions{
		EnableClientCredentials:    false,
		EnableFederatedCredentials: false,
	}, nil
}

// configureConnection set up Azure DevOps with the Azure credential
func (p *AzdoCiProvider) configureConnection(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	provisioningProvider provisioning.Options,
	servicePrincipal *graphsdk.ServicePrincipal,
	authType PipelineAuthType,
	credentials *entraid.AzureCredentials,
) error {
	if authType == "" || authType == AuthTypeFederated {
		// default and federated credentials are set up in credentialOptions
		return nil
	}
	p.credentials = credentials
	// create service connection for client credentials
	details := repoDetails.details.(*AzdoRepositoryDetails)
	org, _, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.Env, p.console)
	if err != nil {
		return err
	}
	pat, _, err := azdo.EnsurePatExists(ctx, p.Env, p.console)
	if err != nil {
		return err
	}
	connection, err := azdo.GetConnection(ctx, org, pat)
	if err != nil {
		return err
	}
	_, err = azdo.CreateServiceConnection(
		ctx, connection, details.projectId, details.projectName, *p.Env, p.credentials, p.console)
	return err
}

// configurePipeline create Azdo pipeline
func (p *AzdoCiProvider) configurePipeline(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	options *configurePipelineOptions,
) (CiPipeline, error) {
	details := repoDetails.details.(*AzdoRepositoryDetails)

	org, _, err := azdo.EnsureOrgNameExists(ctx, p.envManager, p.Env, p.console)
	if err != nil {
		return nil, err
	}
	pat, _, err := azdo.EnsurePatExists(ctx, p.Env, p.console)
	if err != nil {
		return nil, err
	}
	connection, err := azdo.GetConnection(ctx, org, pat)
	if err != nil {
		return nil, err
	}
	buildDefinition, err := azdo.CreatePipeline(
		ctx,
		details.projectId,
		azdo.AzurePipelineName,
		details.repoName,
		connection,
		p.credentials,
		p.Env,
		p.console,
		*options.provisioningProvider,
		options.secrets,
		options.variables,
	)
	if err != nil {
		return nil, err
	}
	details.buildDefinition = buildDefinition

	return &pipeline{
		repoDetails: details,
	}, nil
}

// pipeline is the implementation for a CiPipeline for Azure DevOps
type pipeline struct {
	repoDetails *AzdoRepositoryDetails
}

func (p *pipeline) name() string {
	return *p.repoDetails.buildDefinition.Name
}
func (p *pipeline) url() string {
	repoUrl := p.repoDetails.repoWebUrl
	repoPrefix := strings.Split(repoUrl, "_git")[0]
	return fmt.Sprintf("%s_build?definitionId=%d", repoPrefix, *p.repoDetails.buildDefinition.Id)
}
