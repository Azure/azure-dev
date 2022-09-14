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

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
)

// AzdoHubScmProvider implements ScmProvider using Azure DevOps as the provider
// for source control manager.
type AzdoHubScmProvider struct {
	repoDetails    *AzdoRepositoryDetails
	Env            *environment.Environment
	azdoConnection *azuredevops.Connection
}
type AzdoRepositoryDetails struct {
	projectName string
	orgName     string
	repoName    string
	remoteUrl   string
	sshUrl      string
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
	_, err := ensureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}

	_, err = ensureAzdoOrgNameExists(ctx, p.Env)
	return err
}

// name returns the name of the provider
func (p *AzdoHubScmProvider) name() string {
	return "Azure DevOps"
}

// ***  scmProvider implementation ******
func (p *AzdoHubScmProvider) ensureGitRepositoryExists(ctx context.Context, console input.Console) (string, error) {
	if p.repoDetails != nil && p.repoDetails.repoName != "" {
		return p.repoDetails.remoteUrl, nil
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	repo, err := getAzDoGitRepositoriesInProject(ctx, p.repoDetails.projectName, p.repoDetails.orgName, connection, console)
	if err != nil {
		return "", err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.repoName = *repo.Name
	repoDetails.remoteUrl = *repo.RemoteUrl
	repoDetails.sshUrl = *repo.SshUrl

	remoteParts := strings.Split(repoDetails.remoteUrl, "@")
	if len(remoteParts) < 2 {
		return "", fmt.Errorf("invalid azure devops remote")
	}
	remoteUser := remoteParts[0]
	remoteHost := remoteParts[1]
	pat, err := ensureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return "", err
	}
	updatedRemote := fmt.Sprintf("%s:%s@%s", remoteUser, pat, remoteHost)

	return updatedRemote, nil
}

func (p *AzdoHubScmProvider) getRepoDetails() *AzdoRepositoryDetails {
	if p.repoDetails != nil {
		return p.repoDetails
	}
	repoDetails := &AzdoRepositoryDetails{}
	p.repoDetails = repoDetails
	return p.repoDetails
}

func (p *AzdoHubScmProvider) getAzdoConnection(ctx context.Context) (*azuredevops.Connection, error) {
	if p.azdoConnection != nil {
		return p.azdoConnection, nil
	}

	org, err := ensureAzdoOrgNameExists(ctx, p.Env)
	if err != nil {
		return nil, err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.orgName = org

	pat, err := ensureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return nil, err
	}

	connection := getAzdoConnection(ctx, org, pat)
	return connection, nil
}
func (p *AzdoHubScmProvider) ensureProjectExists(ctx context.Context, console input.Console) (string, error) {
	if p.repoDetails != nil && p.repoDetails.projectName != "" {
		return p.repoDetails.projectName, nil
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
		return "", fmt.Errorf("prompting for azdo project type: %w", err)
	}

	connection, err := p.getAzdoConnection(ctx)
	if err != nil {
		return "", err
	}

	var projectName string
	switch idx {
	// Select from an existing AzDo project
	case 0:
		name, err := getAzdoProjectFromExisting(ctx, connection, console)
		if err != nil {
			return "", err
		}
		projectName = name
	// Create a new AzDo project
	case 1:
		fmt.Println("New Azdo")
	default:
		panic(fmt.Sprintf("unexpected selection index %d", idx))
	}
	_ = idx
	return projectName, nil
}

// configureGitRemote set up or create the git project and git remote
func (p *AzdoHubScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error) {
	projectName, err := p.ensureProjectExists(ctx, console)
	if err != nil {
		return "", err
	}

	repoDetails := p.getRepoDetails()
	repoDetails.projectName = projectName

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

	var remoteUrl string

	switch idx {
	// Select from an existing GitHub project
	case 0:
		remoteUrl, err = p.ensureGitRepositoryExists(ctx, console)
		if err != nil {
			return "", err
		}

	// Create a new project
	case 1:
		fmt.Println("New")
	// Enter a URL directly.
	case 2:
		fmt.Println("Prompt")
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

	inputConsole := input.GetConsole(ctx)
	projectName, err := p.ensureProjectExists(ctx, inputConsole)
	if err != nil {
		return nil, err
	}
	repoDetails := p.getRepoDetails()
	repoDetails.projectName = projectName

	return &gitRepositoryDetails{
		owner:    p.repoDetails.orgName,
		repoName: p.repoDetails.repoName,
		details:  repoDetails,
	}, nil
}

// preventGitPush is nil for Azure DevOps
func (p *AzdoHubScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) (bool, error) {
	return false, errors.New("not implemented")
}

// AzdoCiProvider implements a CiProvider using Azure DevOps to manage CI with azdo pipelines.
type AzdoCiProvider struct {
	Env *environment.Environment
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *AzdoCiProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// preConfigureCheck nil for Azdo
func (p *AzdoCiProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	_, err := ensureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return err
	}

	_, err = ensureAzdoOrgNameExists(ctx, p.Env)
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
	azdEnvironment environment.Environment,
	repoDetails *gitRepositoryDetails,
	provisioningProvider provisioning.Options,
	credentials json.RawMessage,
	console input.Console) error {

	return errors.New("not implemented")
}

// configurePipeline create Azdo pipeline
func (p *AzdoCiProvider) configurePipeline(ctx context.Context) error {
	return errors.New("not implemented")
}
