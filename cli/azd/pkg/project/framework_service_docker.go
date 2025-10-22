// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type DockerProjectOptions struct {
	Path        string                    `yaml:"path,omitempty"        json:"path,omitempty"`
	Context     string                    `yaml:"context,omitempty"     json:"context,omitempty"`
	Platform    string                    `yaml:"platform,omitempty"    json:"platform,omitempty"`
	Target      string                    `yaml:"target,omitempty"      json:"target,omitempty"`
	Registry    osutil.ExpandableString   `yaml:"registry,omitempty"    json:"registry,omitempty"`
	Image       osutil.ExpandableString   `yaml:"image,omitempty"       json:"image,omitempty"`
	Tag         osutil.ExpandableString   `yaml:"tag,omitempty"         json:"tag,omitempty"`
	RemoteBuild bool                      `yaml:"remoteBuild,omitempty" json:"remoteBuild,omitempty"`
	BuildArgs   []osutil.ExpandableString `yaml:"buildArgs,omitempty"   json:"buildArgs,omitempty"`
	// not supported from azure.yaml directly yet. Adding it for Aspire to use it, initially.
	// Aspire would pass the secret keys, which are env vars that azd will set just to run docker build.
	BuildSecrets []string `yaml:"-"                     json:"-"`
	BuildEnv     []string `yaml:"-"                     json:"-"`
	//InMemDockerfile allow projects to specify a dockerfile contents directly instead of a path on disk.
	// This is not supported from azure.yaml.
	// This is used by projects like Aspire that can generate a dockerfile on the fly and don't want to write it to disk.
	// When this is set, whatever value in Path is ignored and the dockerfile contents in this property is used instead.
	InMemDockerfile []byte `yaml:"-"                     json:"-"`
}

type dockerProject struct {
	env                 *environment.Environment
	docker              *docker.Cli
	framework           FrameworkService
	containerHelper     *ContainerHelper
	console             input.Console
	alphaFeatureManager *alpha.FeatureManager
	commandRunner       exec.CommandRunner
}

// NewDockerProject creates a new instance of a Azd project that
// leverages docker for building
func NewDockerProject(
	env *environment.Environment,
	docker *docker.Cli,
	containerHelper *ContainerHelper,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	commandRunner exec.CommandRunner,
) CompositeFrameworkService {
	return &dockerProject{
		env:                 env,
		docker:              docker,
		containerHelper:     containerHelper,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
		commandRunner:       commandRunner,
		framework:           NewNoOpProject(env),
	}
}

// NewDockerProjectAsFrameworkService is the same as NewDockerProject().(FrameworkService) and exists to support our
// use of DI and ServiceLocators, where we sometimes need to resolve this type as a FrameworkService instance instead
// of a CompositeFrameworkService as [NewDockerProject] does.
func NewDockerProjectAsFrameworkService(
	env *environment.Environment,
	docker *docker.Cli,
	containerHelper *ContainerHelper,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	commandRunner exec.CommandRunner,
) FrameworkService {
	return NewDockerProject(env, docker, containerHelper, console, alphaFeatureManager, commandRunner)
}

func (p *dockerProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			// Docker project needs to build a container image
			RequireBuild: true,
		},
	}
}

// Gets the required external tools for the project
func (p *dockerProject) RequiredExternalTools(ctx context.Context, sc *ServiceConfig) []tools.ExternalTool {
	return p.containerHelper.RequiredExternalTools(ctx, sc)
}

// Initializes the docker project
func (p *dockerProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return p.framework.Initialize(ctx, serviceConfig)
}

// Sets the inner framework service used for restore and build command
func (p *dockerProject) SetSource(inner FrameworkService) {
	p.framework = inner
}

// Restores the dependencies for the docker project
func (p *dockerProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig, serviceContext, progress)
}

// Builds the docker project based on the docker options specified within the Service configuration
func (p *dockerProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return p.containerHelper.Build(ctx, serviceConfig, serviceContext, progress)
}

func (p *dockerProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return p.containerHelper.Package(ctx, serviceConfig, serviceContext, progress)
}

func useDotnetPublishForDockerBuild(serviceConfig *ServiceConfig) bool {
	if serviceConfig.useDotNetPublishForDockerBuild != nil {
		return *serviceConfig.useDotNetPublishForDockerBuild
	}

	serviceConfig.useDotNetPublishForDockerBuild = to.Ptr(false)

	if serviceConfig.Language.IsDotNet() {
		projectPath := serviceConfig.Path()

		dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

		dockerfilePath := dockerOptions.Path
		if !filepath.IsAbs(dockerfilePath) {
			s, err := os.Stat(projectPath)
			if err == nil && s.IsDir() {
				dockerfilePath = filepath.Join(projectPath, dockerfilePath)
			} else {
				dockerfilePath = filepath.Join(filepath.Dir(projectPath), dockerfilePath)
			}
		}

		if _, err := os.Stat(dockerfilePath); errors.Is(err, os.ErrNotExist) {
			serviceConfig.useDotNetPublishForDockerBuild = to.Ptr(true)
		}
	}

	return *serviceConfig.useDotNetPublishForDockerBuild
}
