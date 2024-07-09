// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	githubRemote "github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"golang.org/x/exp/slices"
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
	return gitHubDisplayName
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
		func(directoryName string, file fs.DirEntry, e error) error {
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
				newFile, err := p.gitCli.IsUntrackedFile(ctx, gitProjectPath, directoryName)
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

const (
	federatedIdentityIssuer   = "https://token.actions.githubusercontent.com"
	federatedIdentityAudience = "api://AzureADTokenExchange"
)

// GitHubCiProvider implements a CiProvider using GitHub to manage CI pipelines as
// GitHub actions.
type GitHubCiProvider struct {
	env                *environment.Environment
	credentialProvider account.SubscriptionCredentialProvider
	entraIdService     entraid.EntraIdService
	ghCli              github.GitHubCli
	gitCli             git.GitCli
	console            input.Console
	httpClient         httputil.HttpClient
}

func NewGitHubCiProvider(
	env *environment.Environment,
	credentialProvider account.SubscriptionCredentialProvider,
	entraIdService entraid.EntraIdService,
	ghCli github.GitHubCli,
	gitCli git.GitCli,
	console input.Console,
	httpClient httputil.HttpClient) CiProvider {
	return &GitHubCiProvider{
		env:                env,
		credentialProvider: credentialProvider,
		entraIdService:     entraIdService,
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
	return gitHubDisplayName
}

func (p *GitHubCiProvider) credentialOptions(
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
		// Configure federated auth for both main branch and current branch
		branches := []string{repoDetails.branch}
		if !slices.Contains(branches, "main") {
			branches = append(branches, "main")
		}

		repoSlug := repoDetails.owner + "/" + repoDetails.repoName
		credentialSafeName := strings.ReplaceAll(repoSlug, "/", "-")

		federatedCredentials := []*graphsdk.FederatedIdentityCredential{
			{
				Name:        url.PathEscape(fmt.Sprintf("%s-pull_request", credentialSafeName)),
				Issuer:      federatedIdentityIssuer,
				Subject:     fmt.Sprintf("repo:%s:pull_request", repoSlug),
				Description: convert.RefOf("Created by Azure Developer CLI"),
				Audiences:   []string{federatedIdentityAudience},
			},
		}

		for _, branch := range branches {
			branchCredentials := &graphsdk.FederatedIdentityCredential{
				Name:        url.PathEscape(fmt.Sprintf("%s-%s", credentialSafeName, branch)),
				Issuer:      federatedIdentityIssuer,
				Subject:     fmt.Sprintf("repo:%s:ref:refs/heads/%s", repoSlug, branch),
				Description: convert.RefOf("Created by Azure Developer CLI"),
				Audiences:   []string{federatedIdentityAudience},
			}

			federatedCredentials = append(federatedCredentials, branchCredentials)
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

// ***  ciProvider implementation ******

// configureConnection set up GitHub account with Azure Credentials for
// GitHub actions to use a service principal account to log in to Azure
// and make changes on behalf of a user.
func (p *GitHubCiProvider) configureConnection(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	infraOptions provisioning.Options,
	servicePrincipal *graphsdk.ServicePrincipal,
	authType PipelineAuthType,
	credentials *entraid.AzureCredentials,
) error {
	// Default auth type to client-credentials for terraform
	if infraOptions.Provider == provisioning.Terraform && authType == "" {
		authType = AuthTypeClientCredentials
	}

	repoSlug := repoDetails.owner + "/" + repoDetails.repoName
	if authType == AuthTypeClientCredentials {
		err := p.configureClientCredentialsAuth(ctx, infraOptions, repoSlug, servicePrincipal, credentials)
		if err != nil {
			return fmt.Errorf("configuring client credentials auth: %w", err)
		}
	}

	if err := p.setPipelineVariables(ctx, repoSlug, infraOptions, servicePrincipal); err != nil {
		return fmt.Errorf("failed setting pipeline variables: %w", err)
	}

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
	servicePrincipal *graphsdk.ServicePrincipal,
) error {
	for name, value := range map[string]string{
		environment.EnvNameEnvVarName:        p.env.Name(),
		environment.LocationEnvVarName:       p.env.GetLocation(),
		environment.SubscriptionIdEnvVarName: p.env.GetSubscriptionId(),
		environment.TenantIdEnvVarName:       *servicePrincipal.AppOwnerOrganizationId,
		"AZURE_CLIENT_ID":                    servicePrincipal.AppId,
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
	servicePrincipal *graphsdk.ServicePrincipal,
	credentials *entraid.AzureCredentials,
) error {
	/* #nosec G101 - Potential hardcoded credentials - false positive */
	secretName := "AZURE_CREDENTIALS"
	credsJson, err := json.Marshal(credentials)
	if err != nil {
		return fmt.Errorf("failed marshalling azure credentials: %w", err)
	}

	if err := p.ghCli.SetSecret(ctx, repoSlug, secretName, string(credsJson)); err != nil {
		return fmt.Errorf("failed setting %s secret: %w", secretName, err)
	}
	p.console.MessageUxItem(ctx, &ux.CreatedRepoValue{
		Name: secretName,
		Kind: ux.GitHubSecret,
	})

	if infraOptions.Provider == provisioning.Terraform {
		for key, info := range map[string]struct {
			value  string
			secret bool
		}{
			"ARM_TENANT_ID":     {credentials.TenantId, false},
			"ARM_CLIENT_ID":     {credentials.ClientId, false},
			"ARM_CLIENT_SECRET": {credentials.ClientSecret, true},
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

// configurePipeline is a no-op for GitHub, as the pipeline is automatically
// created by creating the workflow files in .github directory.
func (p *GitHubCiProvider) configurePipeline(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	options *configurePipelineOptions,
) (CiPipeline, error) {
	repoSlug := repoDetails.owner + "/" + repoDetails.repoName

	// Variables and Secrets for a gh-actions are independent from the gh-action. They are set on the repository level.
	// We need to clean up the previous values before setting the new ones.
	// By doing this, we are handling:
	// - When a secret is moved to be a variable (or vice versa). Don't leak the previous value on the pipeline.
	// - When there was a previous additional variable/secret set and then it was updated to empty string or unset from .env.
	msg := ""
	var procErr error
	ciSecrets, ciVariables := []string{}, []string{}
	if len(options.projectVariables) > 0 {
		msg = "Setting up project's variables to be used in the pipeline"
		ciSecretsInstance, err := p.ghCli.ListSecrets(ctx, repoSlug)
		if err != nil {
			return nil, fmt.Errorf("unable to get list of repository secrets: %w", err)
		}
		ciVariablesInstance, err := p.ghCli.ListVariables(ctx, repoSlug)
		if err != nil {
			return nil, fmt.Errorf("unable to get list of repository variables: %w", err)
		}
		ciSecrets = ciSecretsInstance
		ciVariables = ciVariablesInstance
		p.console.ShowSpinner(ctx, msg, input.Step)
	}

	defer func() {
		if msg != "" {
			p.console.StopSpinner(ctx, msg, input.GetStepResultFormat(procErr))
		}
		if procErr == nil {
			p.console.MessageUxItem(ctx, &ux.MultilineMessage{
				Lines: []string{
					"",
					"GitHub Action secrets are now configured. You can view GitHub action secrets that were created at this link:",
					output.WithLinkFormat("https://github.com/%s/settings/secrets/actions", repoSlug),
					""},
			})
		}
	}()

	// create map of variables for O(1) lookup during clean up
	variablesAndSecretsMap := make(map[string]string, len(options.projectVariables)+len(options.projectSecrets))
	for _, value := range options.projectVariables {
		variablesAndSecretsMap[value] = value
	}
	for _, value := range options.projectSecrets {
		variablesAndSecretsMap[value] = value
	}

	// iterate the existing secrets on the pipeline and remove the ones matching the project's secrets or variables
	for _, existingSecret := range ciSecrets {
		if _, willBeUpdated := options.secrets[existingSecret]; willBeUpdated {
			// if the secret will be updated, we don't need to delete it
			continue
		}
		// only delete if the secret is defined in the project's secrets or variables (azure.yaml)
		if _, exists := variablesAndSecretsMap[existingSecret]; exists {
			deleteErr := p.ghCli.DeleteSecret(ctx, repoSlug, existingSecret)
			if deleteErr != nil {
				procErr = fmt.Errorf("failed deleting %s secret: %w", existingSecret, deleteErr)
				return nil, procErr
			}
		}
	}
	// iterate the existing variables on the pipeline and remove the ones matching the project's secrets or variables
	for _, existingVariable := range ciVariables {
		if _, willBeUpdated := options.variables[existingVariable]; willBeUpdated {
			// if the variable will be updated, we don't need to delete it
			continue
		}
		// only delete if the variable is defined in the project's secrets or variables (azure.yaml)
		if _, exists := variablesAndSecretsMap[existingVariable]; exists {
			deleteErr := p.ghCli.DeleteVariable(ctx, repoSlug, existingVariable)
			if deleteErr != nil {
				procErr = fmt.Errorf("failed deleting %s variable: %w", existingVariable, deleteErr)
				return nil, procErr
			}
		}
	}

	// set the new variables and secrets
	for key, value := range options.secrets {
		if err := p.ghCli.SetSecret(ctx, repoSlug, key, value); err != nil {
			procErr = fmt.Errorf("failed setting %s secret: %w", key, err)
			return nil, procErr
		}
	}

	for key, value := range options.variables {
		if err := p.ghCli.SetVariable(ctx, repoSlug, key, value); err != nil {
			procErr = fmt.Errorf("failed setting %s secret: %w", key, err)
			return nil, procErr
		}
	}

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
			if github.RunningOnCodespaces() && projectPath != "" && ghGitProtocol == github.GitHttpsProtocolType {
				// For HTTPS, using gh as credential helper will avoid git asking for password
				// Credential helper is only set for codespaces to improve the experience,
				// see more about this here: https://github.com/Azure/azure-dev/issues/2451
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
	currentDirectoryName := filepath.Base(currentPathName)

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new repository OR Hit enter to use this name:",
			DefaultValue: currentDirectoryName,
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
