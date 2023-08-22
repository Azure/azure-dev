// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	githubRemote "github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// GitHubScmProvider implements ScmProvider using GitHub as the provider
// for source control manager.
type GitHubScmProvider struct {
	newGitHubRepoCreated bool
	console              input.Console
	ghCli                github.GitHubCli
	gitCli               git.GitCli
}

func NewGitHubScmProvider(
	console input.Console,
	ghCli github.GitHubCli,
	gitCli git.GitCli,
) ScmProvider {
	return &GitHubScmProvider{
		console: console,
		ghCli:   ghCli,
		gitCli:  gitCli,
	}
}

// ***  subareaProvider implementation ******

// requiredTools return the list of external tools required by
// GitHub provider during its execution.
func (p *GitHubScmProvider) requiredTools(ctx context.Context) ([]tools.ExternalTool, error) {
	return []tools.ExternalTool{p.ghCli}, nil
}

// preConfigureCheck check the current state of external tools and any
// other dependency to be as expected for execution.
func (p *GitHubScmProvider) preConfigureCheck(
	ctx context.Context,
	pipelineManagerArgs PipelineManagerArgs,
	infraOptions provisioning.Options,
	projectPath string,
) (bool, error) {
	return ensureGitHubLogin(ctx, projectPath, p.ghCli, p.gitCli, github.GitHubHostName, p.console)
}

// name returns the name of the provider
func (p *GitHubScmProvider) Name() string {
	return "GitHub"
}

// ***  scmProvider implementation ******

// configureGitRemote uses GitHub cli to guide user on setting a remote url
// for the local git project
func (p *GitHubScmProvider) configureGitRemote(
	ctx context.Context,
	repoPath string,
	remoteName string,
) (string, error) {
	// used to detect when the GitHub has created a new repo
	p.newGitHubRepoCreated = false

	// There are a few ways to configure the remote so offer a choice to the user.
	idx, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "How would you like to configure your git remote to GitHub?",
		Options: []string{
			"Select an existing GitHub project",
			"Create a new private GitHub repository",
			"Enter a remote URL directly",
		},
		DefaultValue: "Create a new private GitHub repository",
	})

	if err != nil {
		return "", fmt.Errorf("prompting for remote configuration type: %w", err)
	}

	var remoteUrl string

	switch idx {
	// Select from an existing GitHub project
	case 0:
		remoteUrl, err = getRemoteUrlFromExisting(ctx, p.ghCli, p.console)
		if err != nil {
			return "", fmt.Errorf("getting remote from existing repository: %w", err)
		}
	// Create a new project
	case 1:
		remoteUrl, err = getRemoteUrlFromNewRepository(ctx, p.ghCli, repoPath, p.console)
		if err != nil {
			return "", fmt.Errorf("getting remote from new repository: %w", err)
		}
		p.newGitHubRepoCreated = true
	// Enter a URL directly.
	case 2:
		remoteUrl, err = getRemoteUrlFromPrompt(ctx, remoteName, p.console)
		if err != nil {
			return "", fmt.Errorf("getting remote from prompt: %w", err)
		}
	default:
		panic(fmt.Sprintf("unexpected selection index %d", idx))
	}

	return remoteUrl, nil
}

// defines the structure of an ssl git remote
var gitHubRemoteGitUrlRegex = regexp.MustCompile(`^git@github\.com:(.*?)(?:\.git)?$`)

// defines the structure of an HTTPS git remote
var gitHubRemoteHttpsUrlRegex = regexp.MustCompile(`^https://(?:www\.)?github\.com/(.*?)(?:\.git)?$`)

// ErrRemoteHostIsNotGitHub the error used when a non GitHub remote is found
var ErrRemoteHostIsNotGitHub = errors.New("not a github host")

// gitRepoDetails extracts the information from a GitHub remote url into general scm concepts
// like owner, name and path
func (p *GitHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	slug := ""
	for _, r := range []*regexp.Regexp{gitHubRemoteGitUrlRegex, gitHubRemoteHttpsUrlRegex} {
		captures := r.FindStringSubmatch(remoteUrl)
		if captures != nil {
			slug = captures[1]
		}
	}
	if slug == "" {
		return nil, ErrRemoteHostIsNotGitHub
	}
	slugParts := strings.Split(slug, "/")
	repoDetails := &gitRepositoryDetails{
		owner:    slugParts[0],
		repoName: slugParts[1],
		remote:   remoteUrl,
	}
	repoDetails.url = fmt.Sprintf(
		"https://github.com/%s/%s",
		repoDetails.owner,
		repoDetails.repoName)

	return repoDetails, nil
}

// preventGitPush validate if GitHub actions are disabled and won't work before pushing
// changes to upstream.
func (p *GitHubScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string) (bool, error) {
	// Don't need to check for preventing push on new created repos
	// Only check when using an existing repo in case github actions are disabled
	if !p.newGitHubRepoCreated {
		slug := gitRepo.owner + "/" + gitRepo.repoName
		return p.notifyWhenGitHubActionsAreDisabled(ctx, gitRepo.gitProjectPath, slug, remoteName, branchName)
	}
	return false, nil
}

func (p *GitHubScmProvider) GitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string) error {
	return p.gitCli.PushUpstream(ctx, gitRepo.gitProjectPath, remoteName, branchName)
}

// enum type for taking a choice after finding GitHub actions disabled.
type gitHubActionsEnablingChoice int

// defines the options upon detecting GitHub actions disabled.
const (
	manualChoice gitHubActionsEnablingChoice = iota
	cancelChoice
)

// enables gitHubActionsEnablingChoice to produce a string value.
func (selection gitHubActionsEnablingChoice) String() string {
	switch selection {
	case manualChoice:
		return "I have manually enabled GitHub Actions. Continue with pushing my changes."
	case cancelChoice:
		return "Exit without pushing my changes. I don't need to run GitHub actions right now."
	}
	panic("Tried to convert invalid input gitHubActionsEnablingChoice to string")
}

// notifyWhenGitHubActionsAreDisabled uses GitHub cli to check if actions are disabled
// or if at least one workflow is not listed. Returns true after interacting with user
// and if user decides to stop a current petition to push changes to upstream.
func (p *GitHubScmProvider) notifyWhenGitHubActionsAreDisabled(
	ctx context.Context,
	gitProjectPath,
	repoSlug string,
	origin string,
	branch string,
) (bool, error) {
	ghActionsInUpstreamRepo, err := p.ghCli.GitHubActionsExists(ctx, repoSlug)
	if err != nil {
		return false, err
	}

	if ghActionsInUpstreamRepo {
		// upstream is already listing GitHub actions.
		// There's no need to check if there are local workflows
		return false, nil
	}

	// Upstream has no GitHub actions listed.
	// See if there's at least one workflow file within .github/workflows
	ghLocalWorkflowFiles := false
	defaultGitHubWorkflowPathLocation := filepath.Join(
		gitProjectPath,
		".github",
		"workflows")
	err = filepath.WalkDir(defaultGitHubWorkflowPathLocation,
		func(folderName string, file fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			fileName := file.Name()
			fileExtension := filepath.Ext(fileName)
			if fileExtension == ".yml" || fileExtension == ".yaml" {
				// ** workflow file found.
				// Now check if this file is already tracked by git.
				// If the file is not tracked, it means this is a new file (never pushed to mainstream)
				// A git untracked file should not be considered as GitHub workflow until it is pushed.
				newFile, err := p.gitCli.IsUntrackedFile(ctx, gitProjectPath, folderName)
				if err != nil {
					return fmt.Errorf("checking workflow file %w", err)
				}
				if !newFile {
					ghLocalWorkflowFiles = true
				}
			}

			return nil
		})

	if err != nil {
		return false, fmt.Errorf("Getting GitHub local workflow files %w", err)
	}

	if ghLocalWorkflowFiles {
		message := fmt.Sprintf("\n%s\n"+
			" - If you forked and cloned a template, enable actions here: %s.\n"+
			" - Otherwise, check the GitHub Actions permissions here: %s.\n",
			output.WithHighLightFormat("GitHub actions are currently disabled for your repository."),
			output.WithHighLightFormat("https://github.com/%s/actions", repoSlug),
			output.WithHighLightFormat("https://github.com/%s/settings/actions", repoSlug))

		p.console.Message(ctx, message)

		rawSelection, err := p.console.Select(ctx, input.ConsoleOptions{
			Message: "What would you like to do now?",
			Options: []string{
				manualChoice.String(),
				cancelChoice.String(),
			},
			DefaultValue: manualChoice.String(),
		})

		if err != nil {
			return false, fmt.Errorf("prompting to enable github actions: %w", err)
		}
		choice := gitHubActionsEnablingChoice(rawSelection)

		if choice == manualChoice {
			return false, nil
		}

		if choice == cancelChoice {
			return true, nil
		}
	}

	return false, nil
}

// GitHubCiProvider implements a CiProvider using GitHub to manage CI pipelines as
// GitHub actions.
type GitHubCiProvider struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	ghCli              github.GitHubCli
	gitCli             git.GitCli
	console            input.Console
	httpClient         httputil.HttpClient
}

func NewGitHubCiProvider(
	env *environment.Environment,
	credentialProvider account.SubscriptionCredentialProvider,
	ghCli github.GitHubCli,
	gitCli git.GitCli,
	console input.Console,
	httpClient httputil.HttpClient) CiProvider {
	return &GitHubCiProvider{
		env:                env,
		credentialProvider: credentialProvider,
		ghCli:              ghCli,
		gitCli:             gitCli,
		console:            console,
		httpClient:         httpClient,
	}
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *GitHubCiProvider) requiredTools(ctx context.Context) ([]tools.ExternalTool, error) {
	return []tools.ExternalTool{p.ghCli}, nil
}

// preConfigureCheck validates that current state of tools and GitHub is as expected to
// execute.
func (p *GitHubCiProvider) preConfigureCheck(
	ctx context.Context,
	pipelineManagerArgs PipelineManagerArgs,
	infraOptions provisioning.Options,
	projectPath string,
) (bool, error) {
	updated, err := ensureGitHubLogin(ctx, projectPath, p.ghCli, p.gitCli, github.GitHubHostName, p.console)
	if err != nil {
		return updated, err
	}

	authType := PipelineAuthType(pipelineManagerArgs.PipelineAuthTypeName)

	// Federated Auth + Terraform is not a supported combination
	if infraOptions.Provider == provisioning.Terraform {
		// Throw error if Federated auth is explicitly requested
		if authType == AuthTypeFederated {
			return false, fmt.Errorf(
				//nolint:lll
				"Terraform does not support federated authentication. To explicitly use client credentials set the %s flag. %w",
				output.WithBackticks("--auth-type client-credentials"),
				ErrAuthNotSupported,
			)
		} else if authType == "" {
			// If not explicitly set, show warning
			p.console.MessageUxItem(
				ctx,
				&ux.WarningMessage{
					//nolint:lll
					Description: "Terraform provisioning does not support federated authentication, defaulting to Service Principal with client ID and client secret.\n",
				},
			)
		}
	}

	return updated, nil
}

// name returns the name of the provider.
func (p *GitHubCiProvider) Name() string {
	return "GitHub"
}

// ***  ciProvider implementation ******

// configureConnection set up GitHub account with Azure Credentials for
// GitHub actions to use a service principal account to log in to Azure
// and make changes on behalf of a user.
func (p *GitHubCiProvider) configureConnection(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	infraOptions provisioning.Options,
	credentials json.RawMessage,
	authType PipelineAuthType,
) error {

	repoSlug := repoDetails.owner + "/" + repoDetails.repoName

	// Configure federated auth for both main branch and current branch
	branches := []string{repoDetails.branch}
	if !slices.Contains(branches, "main") {
		branches = append(branches, "main")
	}

	// Default auth type to client-credentials for terraform
	if infraOptions.Provider == provisioning.Terraform && authType == "" {
		authType = AuthTypeClientCredentials
	}

	var authErr error

	switch authType {
	case AuthTypeClientCredentials:
		authErr = p.configureClientCredentialsAuth(ctx, infraOptions, repoSlug, credentials)
	default:
		authErr = p.configureFederatedAuth(ctx, infraOptions, repoSlug, branches, credentials)
	}

	if authErr != nil {
		return fmt.Errorf("failed configuring authentication: %w", authErr)
	}

	if err := p.setPipelineVariables(ctx, repoSlug, infraOptions); err != nil {
		return fmt.Errorf("failed setting pipeline variables: %w", err)
	}

	p.console.MessageUxItem(ctx, &ux.MultilineMessage{
		Lines: []string{
			"",
			"GitHub Action secrets are now configured. You can view GitHub action secrets that were created at this link:",
			output.WithLinkFormat("https://github.com/%s/settings/secrets/actions", repoSlug),
			""},
	})

	return nil
}

// setPipelineVariables sets all the pipeline variables required for the pipeline to run.  This includes the environment
// variables that the core of AZD uses (AZURE_ENV_NAME) as well as the variables that the provisioning system needs to run
// (AZURE_SUBSCRIPTION_ID, AZURE_LOCATION) as well as scenario specific variables (AZURE_RESOURCE_GROUP for resource group
// scoped deployments, a series of RS_ variables for terraform remote state)
func (p *GitHubCiProvider) setPipelineVariables(
	ctx context.Context,
	repoSlug string,
	infraOptions provisioning.Options,
) error {
	for name, value := range map[string]string{
		environment.EnvNameEnvVarName:        p.env.GetEnvName(),
		environment.LocationEnvVarName:       p.env.GetLocation(),
		environment.SubscriptionIdEnvVarName: p.env.GetSubscriptionId(),
	} {
		if err := p.ghCli.SetVariable(ctx, repoSlug, name, value); err != nil {
			return fmt.Errorf("failed setting %s variable: %w", name, err)
		}
		p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
			Name: name,
			Kind: ux.GitHubVariable,
		})
	}

	if infraOptions.Provider == provisioning.Terraform {
		remoteStateKeys := []string{"RS_RESOURCE_GROUP", "RS_STORAGE_ACCOUNT", "RS_CONTAINER_NAME"}
		for _, key := range remoteStateKeys {
			value, ok := p.env.LookupEnv(key)
			if !ok || strings.TrimSpace(value) == "" {
				p.console.StopSpinner(ctx, "Configuring terraform", input.StepWarning)
				p.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Terraform Remote State configuration is invalid",
					HidePrefix:  true,
				})
				p.console.Message(
					ctx,
					fmt.Sprintf(
						"Visit %s for more information on configuring Terraform remote state",
						output.WithLinkFormat("https://aka.ms/azure-dev/terraform"),
					),
				)
				p.console.Message(ctx, "")
				return errors.New("terraform remote state is not correctly configured")
			}

			// env var was found
			if err := p.ghCli.SetVariable(ctx, repoSlug, key, value); err != nil {
				return fmt.Errorf("setting terraform remote state variables: %w", err)
			}
			p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
				Name: key,
				Kind: ux.GitHubVariable,
			})
		}
	}

	if infraOptions.Provider == provisioning.Bicep {
		if rgName, has := p.env.LookupEnv(environment.ResourceGroupEnvVarName); has {
			if err := p.ghCli.SetVariable(ctx, repoSlug, environment.ResourceGroupEnvVarName, rgName); err != nil {
				return fmt.Errorf("failed setting %s variable: %w", environment.ResourceGroupEnvVarName, err)
			}
		}
	}

	return nil
}

// Configures Github for standard Service Principal authentication with client id & secret
func (p *GitHubCiProvider) configureClientCredentialsAuth(
	ctx context.Context,
	infraOptions provisioning.Options,
	repoSlug string,
	credentials json.RawMessage,
) error {
	/* #nosec G101 - Potential hardcoded credentials - false positive */
	secretName := "AZURE_CREDENTIALS"
	if err := p.ghCli.SetSecret(ctx, repoSlug, secretName, string(credentials)); err != nil {
		return fmt.Errorf("failed setting %s secret: %w", secretName, err)
	}
	p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
		Name: secretName,
		Kind: ux.GitHubSecret,
	})

	if infraOptions.Provider == provisioning.Terraform {
		// terraform expect the credential info to be set in the env individually
		type credentialParse struct {
			Tenant       string `json:"tenantId"`
			ClientId     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
		}
		values := credentialParse{}
		if e := json.Unmarshal(credentials, &values); e != nil {
			return fmt.Errorf("setting terraform env var credentials: %w", e)
		}

		for key, info := range map[string]struct {
			value  string
			secret bool
		}{
			"ARM_TENANT_ID":     {values.Tenant, false},
			"ARM_CLIENT_ID":     {values.ClientId, false},
			"ARM_CLIENT_SECRET": {values.ClientSecret, true},
		} {
			if !info.secret {
				if err := p.ghCli.SetVariable(ctx, repoSlug, key, info.value); err != nil {
					return fmt.Errorf("setting github variable %s:: %w", key, err)
				}
				p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
					Name: key,
					Kind: ux.GitHubVariable,
				})
			} else {
				if err := p.ghCli.SetSecret(ctx, repoSlug, key, info.value); err != nil {
					return fmt.Errorf("setting github secret %s:: %w", key, err)
				}
				p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
					Name: key,
					Kind: ux.GitHubSecret,
				})
			}
		}
	}

	return nil
}

// Configures Github for federated authentication using registered application with federated identity credentials
func (p *GitHubCiProvider) configureFederatedAuth(
	ctx context.Context,
	infraOptions provisioning.Options,
	repoSlug string,
	branches []string,
	credentials json.RawMessage,
) error {
	var azureCredentials azcli.AzureCredentials
	if err := json.Unmarshal(credentials, &azureCredentials); err != nil {
		return fmt.Errorf("failed unmarshalling azure credentials: %w", err)
	}

	credential, err := p.credentialProvider.CredentialForSubscription(ctx, azureCredentials.SubscriptionId)
	if err != nil {
		return err
	}

	err = applyFederatedCredentials(ctx, repoSlug, branches, &azureCredentials, p.console, p.httpClient, credential)
	if err != nil {
		return err
	}

	for key, value := range map[string]string{
		environment.TenantIdEnvVarName: azureCredentials.TenantId,
		"AZURE_CLIENT_ID":              azureCredentials.ClientId,
	} {
		if err := p.ghCli.SetVariable(ctx, repoSlug, key, value); err != nil {
			return fmt.Errorf("failed setting github variable '%s':  %w", key, err)
		}
		p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
			Name: key,
			Kind: ux.GitHubVariable,
		})
	}

	return nil
}

const (
	federatedIdentityIssuer   = "https://token.actions.githubusercontent.com"
	federatedIdentityAudience = "api://AzureADTokenExchange"
)

func applyFederatedCredentials(
	ctx context.Context,
	repoSlug string,
	branches []string,
	azureCredentials *azcli.AzureCredentials,
	console input.Console,
	httpClient httputil.HttpClient,
	credential azcore.TokenCredential,
) error {
	graphClient, err := createGraphClient(ctx, httpClient, credential)
	if err != nil {
		return err
	}

	appsResponse, err := graphClient.
		Applications().
		Filter(fmt.Sprintf("appId eq '%s'", azureCredentials.ClientId)).
		Get(ctx)
	if err != nil || len(appsResponse.Value) == 0 {
		return fmt.Errorf("failed finding matching application: %w", err)
	}

	application := appsResponse.Value[0]

	existingCredsResponse, err := graphClient.
		ApplicationById(*application.Id).
		FederatedIdentityCredentials().
		Get(ctx)

	if err != nil {
		return fmt.Errorf("failed retrieving federated credentials: %w", err)
	}

	credentialSafeName := strings.ReplaceAll(repoSlug, "/", "-")

	// List of desired federated credentials
	federatedCredentials := []graphsdk.FederatedIdentityCredential{
		{
			Name:        url.PathEscape(fmt.Sprintf("%s-pull_request", credentialSafeName)),
			Issuer:      federatedIdentityIssuer,
			Subject:     fmt.Sprintf("repo:%s:pull_request", repoSlug),
			Description: convert.RefOf("Created by Azure Developer CLI"),
			Audiences:   []string{federatedIdentityAudience},
		},
	}

	for _, branch := range branches {
		branchCredentials := graphsdk.FederatedIdentityCredential{
			Name:        url.PathEscape(fmt.Sprintf("%s-%s", credentialSafeName, branch)),
			Issuer:      federatedIdentityIssuer,
			Subject:     fmt.Sprintf("repo:%s:ref:refs/heads/%s", repoSlug, branch),
			Description: convert.RefOf("Created by Azure Developer CLI"),
			Audiences:   []string{federatedIdentityAudience},
		}

		federatedCredentials = append(federatedCredentials, branchCredentials)
	}

	// Ensure the credential exists otherwise create a new one.
	for i := range federatedCredentials {
		err := ensureFederatedCredential(
			ctx, graphClient, &application, existingCredsResponse.Value, &federatedCredentials[i], console)
		if err != nil {
			return err
		}
	}

	return nil
}

// configurePipeline is a no-op for GitHub, as the pipeline is automatically
// created by creating the workflow files in .github folder.
func (p *GitHubCiProvider) configurePipeline(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	provisioningProvider provisioning.Options,
) (CiPipeline, error) {
	return &workflow{
		repoDetails: repoDetails,
	}, nil
}

// workflow is the implementation for a CiPipeline for GitHub
type workflow struct {
	repoDetails *gitRepositoryDetails
}

func (w *workflow) name() string {
	return "actions"
}
func (w *workflow) url() string {
	return w.repoDetails.url + "/actions"
}

// ensureGitHubLogin ensures the user is logged into the GitHub CLI. If not, it prompt the user
// if they would like to log in and if so runs `gh auth login` interactively.
func ensureGitHubLogin(
	ctx context.Context,
	projectPath string,
	ghCli github.GitHubCli,
	gitCli git.GitCli,
	hostname string,
	console input.Console) (bool, error) {
	authResult, err := ghCli.GetAuthStatus(ctx, hostname)
	if err != nil {
		return false, err
	}

	if authResult.LoggedIn {
		return false, nil
	}

	for {
		var accept bool
		accept, err := console.Confirm(ctx, input.ConsoleOptions{
			Message:      "This command requires you to be logged into GitHub. Log in using the GitHub CLI?",
			DefaultValue: true,
		})
		if err != nil {
			return false, fmt.Errorf("prompting to log in to github: %w", err)
		}

		if !accept {
			return false, errors.New("interactive GitHub login declined; use `gh auth login` to log into GitHub")
		}

		ghGitProtocol, err := ghCli.GetGitProtocolType(ctx)
		if err != nil {
			return false, err
		}

		if err := ghCli.Login(ctx, hostname); err == nil {
			if projectPath != "" && ghGitProtocol == github.GitHttpsProtocolType {
				// For HTTPS, using gh as credential helper will avoid git asking for password
				if err := gitCli.SetGitHubAuthForRepo(
					ctx, projectPath, fmt.Sprintf("https://%s", hostname), ghCli.BinaryPath()); err != nil {
					return false, err
				}
			}
			return true, nil
		}

		fmt.Fprintln(console.Handles().Stdout, "There was an issue logging into GitHub.")
	}
}

// getRemoteUrlFromExisting let user to select an existing repository from his/her account and
// returns the remote url for that repository.
func getRemoteUrlFromExisting(ctx context.Context, ghCli github.GitHubCli, console input.Console) (string, error) {
	repos, err := ghCli.ListRepositories(ctx)
	if err != nil {
		return "", fmt.Errorf("listing existing repositories: %w", err)
	}

	options := make([]string, 0, len(repos))
	for _, repo := range repos {
		options = append(options, repo.NameWithOwner)
	}

	if len(options) == 0 {
		return "", errors.New("no existing GitHub repositories found")
	}

	repoIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Choose an existing GitHub repository",
		Options: options,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for repository: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repos[repoIdx])
}

// selectRemoteUrl let user to type and enter the url from an existing GitHub repo.
// If the url is valid, the remote url is returned. Otherwise an error is returned.
func selectRemoteUrl(ctx context.Context, ghCli github.GitHubCli, repo github.GhCliRepository) (string, error) {
	protocolType, err := ghCli.GetGitProtocolType(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting default protocol: %w", err)
	}

	switch protocolType {
	case github.GitHttpsProtocolType:
		return repo.HttpsUrl, nil
	case github.GitSshProtocolType:
		return repo.SshUrl, nil
	default:
		panic(fmt.Sprintf("unexpected protocol type: %s", protocolType))
	}
}

// getRemoteUrlFromNewRepository creates a new repository on GitHub and returns its remote url
func getRemoteUrlFromNewRepository(
	ctx context.Context,
	ghCli github.GitHubCli,
	currentPathName string,
	console input.Console,
) (string, error) {
	var repoName string
	currentFolderName := filepath.Base(currentPathName)

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new repository OR Hit enter to use this name:",
			DefaultValue: currentFolderName,
		})
		if err != nil {
			return "", fmt.Errorf("asking for new repository name: %w", err)
		}

		err = ghCli.CreatePrivateRepository(ctx, name)
		if errors.Is(err, github.ErrRepositoryNameInUse) {
			console.Message(ctx, fmt.Sprintf("error: the repository name '%s' is already in use\n", name))
			continue // try again
		} else if err != nil {
			return "", fmt.Errorf("creating repository: %w", err)
		} else {
			repoName = name
			break
		}
	}

	repo, err := ghCli.ViewRepository(ctx, repoName)
	if err != nil {
		return "", fmt.Errorf("fetching repository info: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repo)
}

// getRemoteUrlFromPrompt interactively prompts the user for a URL for a GitHub repository. It validates
// that the URL is well formed and is in the correct format for a GitHub repository.
func getRemoteUrlFromPrompt(ctx context.Context, remoteName string, console input.Console) (string, error) {
	remoteUrl := ""

	for remoteUrl == "" {
		promptValue, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Enter the url to use for remote %s:", remoteName),
		})

		if err != nil {
			return "", fmt.Errorf("prompting for remote url: %w", err)
		}

		remoteUrl = promptValue

		if _, err := githubRemote.GetSlugForRemote(remoteUrl); errors.Is(err, githubRemote.ErrRemoteHostIsNotGitHub) {
			fmt.Fprintf(console.Handles().Stdout, "error: \"%s\" is not a valid GitHub URL.\n", remoteUrl)

			// So we retry from the loop.
			remoteUrl = ""
		}
	}

	return remoteUrl, nil
}

// Ensures that the federated credential exists on the application otherwise create a new one
func ensureFederatedCredential(
	ctx context.Context,
	graphClient *graphsdk.GraphClient,
	application *graphsdk.Application,
	existingCredentials []graphsdk.FederatedIdentityCredential,
	repoCredential *graphsdk.FederatedIdentityCredential,
	console input.Console,
) error {
	// If a federated credential already exists for the same subject then nothing to do.
	for _, existing := range existingCredentials {
		if existing.Subject == repoCredential.Subject {
			log.Printf(
				"federated credential with subject '%s' already exists on application '%s'",
				repoCredential.Subject,
				*application.Id,
			)
			return nil
		}
	}

	// Otherwise create the new federated credential
	_, err := graphClient.
		ApplicationById(*application.Id).
		FederatedIdentityCredentials().
		Post(ctx, repoCredential)

	if err != nil {
		return fmt.Errorf("failed creating federated credential: %w", err)
	}

	console.MessageUxItem(
		ctx,
		&ux.DisplayedResource{
			Type: "Federated identity credential for GitHub",
			Name: fmt.Sprintf("subject %s", repoCredential.Subject),
		},
	)

	return nil
}

func createGraphClient(
	ctx context.Context,
	httpClient httputil.HttpClient,
	credential azcore.TokenCredential) (*graphsdk.GraphClient, error) {
	graphOptions := azsdk.
		NewClientOptionsBuilder().
		WithTransport(httpClient).
		BuildCoreClientOptions()

	return graphsdk.NewGraphClient(credential, graphOptions)
}
