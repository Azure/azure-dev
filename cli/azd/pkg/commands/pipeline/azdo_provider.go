// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// AzdoHubScmProvider implements ScmProvider using Azure DevOps as the provider
// for source control manager.
type AzdoHubScmProvider struct {
	projectName string
	Env         *environment.Environment
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
func (p *AzdoHubScmProvider) ensureProjectExists(ctx context.Context, console input.Console) (string, error) {
	org, err := ensureAzdoOrgNameExists(ctx, p.Env)
	if err != nil {
		return "", err
	}

	pat, err := ensureAzdoPatExists(ctx, p.Env)
	if err != nil {
		return "", err
	}

	connection := getAzdoConnection(ctx, org, pat)

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

func (p *AzdoHubScmProvider) supportsProjects() bool {
	return true
}

// configureGitRemote set up or create the git project and git remote
func (p *AzdoHubScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error) {

	return "remoteUrl", errors.New("not implemented")
}

// gitRepoDetails extracts the information from an Azure DevOps remote url into general scm concepts
// like owner, name and path
func (p *AzdoHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	inputConsole := input.GetConsole(ctx)
	projectName, err := p.ensureProjectExists(ctx, inputConsole)
	if err != nil {
		return nil, err
	}
	p.projectName = projectName
	return &gitRepositoryDetails{
		owner:    "",
		repoName: "",
	}, errors.New("not implemented")
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
