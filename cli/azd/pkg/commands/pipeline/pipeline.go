// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type subareaProvider interface {
	requiredTools() []tools.ExternalTool
	preConfigureCheck(ctx context.Context, console input.Console) error
	name() string
}

type gitRepositoryDetails struct {
	owner          string
	repoName       string
	gitProjectPath string
}

type ScmProvider interface {
	subareaProvider
	gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error)
	// configureGitRemote returns the git repository url after setting it
	configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error)
	preventGitPush(
		ctx context.Context,
		gitRepo *gitRepositoryDetails,
		remoteName string,
		branchName string,
		console input.Console) (bool, error)
}

type CiProvider interface {
	subareaProvider
	configurePipeline(ctx context.Context) error
	configureConnection(
		ctx context.Context,
		azdEnvironment environment.Environment,
		gitRepo *gitRepositoryDetails,
		credential json.RawMessage) error
}
