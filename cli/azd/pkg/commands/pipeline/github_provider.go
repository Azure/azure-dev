// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type gitHubScmProvider struct {
}

// ***  subareaProvider implementation ******
func (p *gitHubScmProvider) requiredTools() []tools.ExternalTool {
	return nil
}

func (p *gitHubScmProvider) preConfigureCheck(ctx context.Context) error {
	return nil
}

func (p *gitHubScmProvider) name() string {
	return "GitHub"
}

// ***  scmProvider implementation ******
func (p *gitHubScmProvider) configureGitRemote(branchName string) (string, error) {
	return "", nil
}

func (p *gitHubScmProvider) preventGitPush(
	ctx context.Context,
	repoSlug string,
	remoteName string,
	branchName string,
	console input.Console) (bool, error) {
	return false, nil
}

type gitHubCiProvider struct {
}

// ***  subareaProvider implementation ******
func (p *gitHubCiProvider) requiredTools() []tools.ExternalTool {
	return nil
}

func (p *gitHubCiProvider) preConfigureCheck(ctx context.Context) error {
	return nil
}
func (p *gitHubCiProvider) name() string {
	return "GitHub"
}

// ***  ciProvider implementation ******
func (p *gitHubCiProvider) configureConnection(
	ctx context.Context,
	repoSlug string,
	environmentName string,
	location string,
	subscriptionId string,
	credential json.RawMessage) error {
	return nil
}
