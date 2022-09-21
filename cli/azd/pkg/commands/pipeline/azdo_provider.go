// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// AzdoHubScmProvider implements ScmProvider using Azure DevOps as the provider
// for source control manager.
type AzdoHubScmProvider struct {
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
	return errors.New("not implemented")
}

// name returns the name of the provider
func (p *AzdoHubScmProvider) name() string {
	return "Azure DevOps"
}

// ***  scmProvider implementation ******

// configureGitRemote set up or create the git project and git remote
func (p *AzdoHubScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error) {
	return "remoteUrl", errors.New("not implemented")
}

// gitRepoDetails extracts the information from an Azure DevOps remote url into general scm concepts
// like owner, name and path
func (p *AzdoHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
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
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *AzdoCiProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// preConfigureCheck nil for Azdo
func (p *AzdoCiProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	return errors.New("not implemented")
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

	return errors.New("not implemented")
}

// configurePipeline create Azdo pipeline
func (p *AzdoCiProvider) configurePipeline(ctx context.Context) error {
	return errors.New("not implemented")
}
