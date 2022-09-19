// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/build"
	azdoGit "github.com/microsoft/azure-devops-go-api/azuredevops/git"
)

// AzdoHubScmProvider implements ScmProvider using Azure DevOps as the provider
// for source control manager.
type AzdoHubScmProvider struct {
	repoDetails    *AzdoRepositoryDetails
	Env            *environment.Environment
	AzdContext     *azdcontext.AzdContext
	azdoConnection *azuredevops.Connection
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
func (p *AzdoHubScmProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// preConfigureCheck check the current state of external tools and any
// other dependency to be as expected for execution.
func (p *AzdoHubScmProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	_, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}

	_, err = azdo.EnsureAzdoOrgNameExists(ctx, p.Env)
	return err
}

// helper function to save configuration values to .env file
func (p *AzdoHubScmProvider) saveEnvironmentConfig(key string, value string) error {
	p.Env.Values[key] = value
	err := p.Env.Save()
	if err != nil {
		return err
	}
	return nil
}

// name returns the name of the provider
func (p *AzdoHubScmProvider) name() string {
	return "Azure DevOps"
}

// ***  scmProvider implementation ******

// stores repo details in state for use in other functions. Also saves AzDo project details to .env
func (p *AzdoHubScmProvider) StoreRepoDetails(ctx context.Context, repo *azdoGit.GitRepository) error {
	repoDetails := p.getRepoDetails()
	repoDetails.repoName = *repo.Name
	repoDetails.remoteUrl = *repo.RemoteUrl
	repoDetails.repoWebUrl = *repo.WebUrl
	repoDetails.sshUrl = *repo.SshUrl
	repoDetails.repoId = repo.Id.String()

	err := p.saveEnvironmentConfig(azdo.AzDoEnvironmentRepoIdName, p.repoDetails.repoId)
	if err != nil {
		return fmt.Errorf("error saving repo id to environment %w", err)
	}

	err = p.saveEnvironmentConfig(azdo.AzDoEnvironmentRepoName, p.repoDetails.repoName)
	if err != nil {
		return fmt.Errorf("error saving repo name to environment %w", err)
	}

	err = p.saveEnvironmentConfig(azdo.AzDoEnvironmentRepoWebUrl, p.repoDetails.repoWebUrl)
	if err != nil {
		return fmt.Errorf("error saving repo web url to environment %w", err)
	}

	return nil
}

// prompts the user for a new AzDo Git repo and creates the repo
func (p *AzdoHubScmProvider) createNewGitRepositoryFromInput(ctx context.Context, console input.Console) (string, error) {
	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	var repo *azdoGit.GitRepository
	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new Azure Devops Repository OR Hit enter to use this name:",
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
			console.Message(ctx, fmt.Sprintf("error: '%s' is not a valid Azure Devops repo name. See https://aka.ms/azure-dev/azdo-repo-naming\n", name))
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
func (p *AzdoHubScmProvider) ensureGitRepositoryExists(ctx context.Context, console input.Console) (string, error) {
	if p.repoDetails != nil && p.repoDetails.repoName != "" {
		return p.repoDetails.remoteUrl, nil
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	repo, err := azdo.GetAzDoGitRepositoriesInProject(ctx, p.repoDetails.projectName, p.repoDetails.orgName, connection, console)
	if err != nil {
		return "", err
	}

	err = p.StoreRepoDetails(ctx, repo)
	if err != nil {
		return "", err
	}

	remoteParts := strings.Split(p.repoDetails.remoteUrl, "@")
	if len(remoteParts) < 2 {
		return "", fmt.Errorf("invalid azure devops remote")
	}
	remoteUser := remoteParts[0]
	remoteHost := remoteParts[1]
	pat, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return "", err
	}

	updatedRemote := fmt.Sprintf("%s:%s@%s", remoteUser, pat, remoteHost)
	console.Message(ctx, fmt.Sprintf("using azure devOps repo: %s", p.repoDetails.repoWebUrl))

	return updatedRemote, nil
}

// helper function to return repoDetails from state
func (p *AzdoHubScmProvider) getRepoDetails() *AzdoRepositoryDetails {
	if p.repoDetails != nil {
		return p.repoDetails
	}
	repoDetails := &AzdoRepositoryDetails{}
	p.repoDetails = repoDetails
	return p.repoDetails
}

// helper function to return an azuredevops.Connection for use with AzDo Go SDK
func (p *AzdoHubScmProvider) getAzdoConnection(ctx context.Context) (*azuredevops.Connection, error) {
	if p.azdoConnection != nil {
		return p.azdoConnection, nil
	}

	org, err := azdo.EnsureAzdoOrgNameExists(ctx, p.Env)
	if err != nil {
		return nil, err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.orgName = org

	pat, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return nil, err
	}

	connection, err := azdo.GetAzdoConnection(ctx, org, pat)
	if err != nil {
		return nil, err
	}

	return connection, nil
}

// returns an existing project or prompts the user to either select a project or a create a new AzDo project
func (p *AzdoHubScmProvider) ensureProjectExists(ctx context.Context, console input.Console) (string, string, bool, error) {
	if p.repoDetails != nil && p.repoDetails.projectName != "" {
		return p.repoDetails.projectName, p.repoDetails.projectId, false, nil
	}
	idx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How would you like to configure your project?",
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
		projectName, projectId, err = azdo.GetAzdoProjectFromExisting(ctx, connection, console)
		if err != nil {
			return "", "", false, err
		}
	// Create a new AzDo project
	case 1:
		projectName, projectId, err = azdo.GetAzdoProjectFromNew(ctx, p.AzdContext.ProjectDirectory(), connection, p.Env, console)
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
func (p *AzdoHubScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error) {
	projectName, projectId, newProject, err := p.ensureProjectExists(ctx, console)
	if err != nil {
		return "", err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.projectName = projectName
	repoDetails.projectId = projectId

	err = p.saveEnvironmentConfig(azdo.AzDoEnvironmentProjectIdName, projectId)
	if err != nil {
		return "", fmt.Errorf("error saving project id to environment %w", err)
	}

	err = p.saveEnvironmentConfig(azdo.AzDoEnvironmentProjectName, projectName)
	if err != nil {
		return "", fmt.Errorf("error saving project name to environment %w", err)
	}
	var remoteUrl string

	if !newProject {
		remoteUrl, err = p.promptForAzdoRepository(ctx, console)
		if err != nil {
			return "", err
		}
	} else {
		remoteUrl, err = p.getDefaultRepoRemote(ctx, projectName, projectId, console)
		if err != nil {
			return "", err
		}
	}
	return remoteUrl, nil
}

// returns the git remote for a newly created repo that is part of a newly created AzDo project
func (p *AzdoHubScmProvider) getDefaultRepoRemote(ctx context.Context, projectName string, projectId string, console input.Console) (string, error) {
	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}
	repo, err := azdo.GetAzDoDefaultGitRepositoriesInProject(ctx, projectName, connection)
	if err != nil {
		return "", err
	}

	err = p.StoreRepoDetails(ctx, repo)
	if err != nil {
		return "", err
	}

	message := fmt.Sprintf("using default repo (%s) in newly created project(%s)", *repo.Name, projectName)
	console.Message(ctx, message)

	return *repo.RemoteUrl, nil
}

// prompt the user to select azdo repo or create a new one
func (p *AzdoHubScmProvider) promptForAzdoRepository(ctx context.Context, console input.Console) (string, error) {
	var remoteUrl string
	// There are a few ways to configure the remote so offer a choice to the user.
	idx, err := console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("How would you like to configure your remote? (Organization: %s)", p.repoDetails.projectName),
		Options: []string{
			"Select an existing Azure Devops Repository",
			"Create a new private Azure Devops Repository",
		},
		DefaultValue: "Create a new private Azure Devops Repository",
	})

	if err != nil {
		return "", fmt.Errorf("prompting for remote configuration type: %w", err)
	}

	switch idx {
	// Select from an existing Azure Devops project
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
var ErrRemoteHostIsNotAzDo = errors.New("existing remote is not an azure devops host")

// helper function to determine if the provided remoteUrl is an azure devops repo.
// currently supports AzDo PaaS
func isAzDoRemote(remoteUrl string) error {
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

// gitRepoDetails extracts the information from an Azure DevOps remote url into general scm concepts
// like owner, name and path
func (p *AzdoHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	err := isAzDoRemote(remoteUrl)
	if err != nil {
		return nil, err
	}

	repoDetails := p.getRepoDetails()
	if repoDetails.orgName == "" {
		repoDetails.orgName = p.Env.Values[azdo.AzDoEnvironmentOrgName]
	}
	if repoDetails.projectName == "" {
		repoDetails.projectName = p.Env.Values[azdo.AzDoEnvironmentProjectName]
	}
	if repoDetails.projectId == "" {
		repoDetails.projectId = p.Env.Values[azdo.AzDoEnvironmentProjectIdName]
	}
	if repoDetails.repoName == "" {
		repoDetails.repoName = p.Env.Values[azdo.AzDoEnvironmentRepoName]
	}
	if repoDetails.repoId == "" {
		repoDetails.repoId = p.Env.Values[azdo.AzDoEnvironmentRepoIdName]
	}
	if repoDetails.repoWebUrl == "" {
		repoDetails.repoWebUrl = p.Env.Values[azdo.AzDoEnvironmentRepoWebUrl]
	}
	if repoDetails.remoteUrl == "" {
		repoDetails.remoteUrl = remoteUrl
	}

	return &gitRepositoryDetails{
		owner:    p.repoDetails.orgName,
		repoName: p.repoDetails.repoName,
		details:  repoDetails,
	}, nil
}

// preventGitPush is nil for Azure DevOps
// this method also checks for a credential helper and prompts the user to set a helper to make subsequent pushes easier
func (p *AzdoHubScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) (bool, error) {

	gitCli := git.NewGitCli(ctx)
	foundHelper, err := gitCli.CheckConfigCredentialHelper(ctx)
	if err != nil {
		return false, err
	}
	if !foundHelper {
		console.Message(ctx, `  
  A credential helper is not configured for git.
  This will require you to enter your Azure DevOps PAT when executing a git push.
  https://aka.ms/azure-dev/git-store
  `)
		idx, err := console.Select(ctx, input.ConsoleOptions{
			Message: "Would you like to enable credential.helper store for this local git repository?",
			Options: []string{
				"Yes - set credential.helper = store",
				"No - do not configure credential.helper",
			},
			DefaultValue: "Yes - set credential.helper = store",
		})
		if err != nil {
			return false, fmt.Errorf("prompting for credential helper: %w ", err)
		}
		switch idx {
		// Configure Credential Store
		case 0:
			err = gitCli.SetCredentialStore(ctx, p.AzdContext.ProjectDirectory())
			if err != nil {
				return false, fmt.Errorf("storing credentials in env: %w ", err)
			}
		// Skip
		case 1:
			break
		default:
			panic(fmt.Sprintf("unexpected selection index %d", idx))
		}
	}

	return false, nil
}

// hook function that fires after a git push
// allows the provider to perform certain tasks after push including
// cleanup on the remote url, creating the build policy for PRs and queuing an initial deployment
func (p *AzdoHubScmProvider) postGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) error {

	gitCli := git.NewGitCli(ctx)

	//Reset remote to original url without PAT
	err := gitCli.UpdateRemote(ctx, p.AzdContext.ProjectDirectory(), remoteName, p.repoDetails.remoteUrl)
	if err != nil {
		return err
	}
	if gitRepo.pushStatus {
		console.Message(ctx, output.WithSuccessFormat(azdo.AzdoConfigSuccessMessage, p.repoDetails.repoWebUrl))
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return err
	}

	err = azdo.CreateBuildPolicy(ctx, connection, p.repoDetails.projectId, p.repoDetails.repoId, p.repoDetails.buildDefinition)
	if err != nil {
		return err
	}

	err = azdo.QueueBuild(ctx, connection, p.repoDetails.projectId, p.repoDetails.buildDefinition)
	if err != nil {
		return err
	}

	return nil
}

// AzdoCiProvider implements a CiProvider using Azure DevOps to manage CI with azdo pipelines.
type AzdoCiProvider struct {
	Env         *environment.Environment
	AzdContext  *azdcontext.AzdContext
	credentials *azdo.AzureServicePrincipalCredentials
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *AzdoCiProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// preConfigureCheck nil for Azdo
func (p *AzdoCiProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	_, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}

	_, err = azdo.EnsureAzdoOrgNameExists(ctx, p.Env)
	return err
}

// name returns the name of the provider.
func (p *AzdoCiProvider) name() string {
	return "Azure DevOps"
}

// ***  ciProvider implementation ******

// configureConnection set up Azure DevOps with the Azure credential
func (p *AzdoCiProvider) configureConnection(
	ctx context.Context,
	azdEnvironment *environment.Environment,
	repoDetails *gitRepositoryDetails,
	provisioningProvider provisioning.Options,
	credentials json.RawMessage,
	console input.Console) error {

	azureCredentials, err := parseCredentials(ctx, credentials)
	if err != nil {
		return err
	}

	p.credentials = azureCredentials
	details := repoDetails.details.(*AzdoRepositoryDetails)
	org, err := azdo.EnsureAzdoOrgNameExists(ctx, p.Env)
	if err != nil {
		return err
	}
	pat, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}
	connection, err := azdo.GetAzdoConnection(ctx, org, pat)
	if err != nil {
		return err
	}
	err = azdo.CreateServiceConnection(ctx, connection, details.projectId, *p.Env, *p.credentials, console)
	if err != nil {
		return err
	}
	return nil
}

// struct used to deserialize service principal json object

// parses the incoming json object and deserializes it to a struct
func parseCredentials(ctx context.Context, credentials json.RawMessage) (*azdo.AzureServicePrincipalCredentials, error) {
	azureCredentials := azdo.AzureServicePrincipalCredentials{}
	if e := json.Unmarshal(credentials, &azureCredentials); e != nil {
		return nil, fmt.Errorf("setting terraform env var credentials: %w", e)
	}
	return &azureCredentials, nil
}

// configurePipeline create Azdo pipeline
func (p *AzdoCiProvider) configurePipeline(ctx context.Context, repoDetails *gitRepositoryDetails) error {
	details := repoDetails.details.(*AzdoRepositoryDetails)

	org, err := azdo.EnsureAzdoOrgNameExists(ctx, p.Env)
	if err != nil {
		return err
	}
	pat, err := azdo.EnsureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}
	connection, err := azdo.GetAzdoConnection(ctx, org, pat)
	if err != nil {
		return err
	}
	buildDefinition, err := azdo.CreatePipeline(ctx, details.projectId, azdo.AzurePipelineName, details.repoName, connection, *p.credentials, *p.Env)
	if err != nil {
		return err
	}
	details.buildDefinition = buildDefinition
	return nil
}
