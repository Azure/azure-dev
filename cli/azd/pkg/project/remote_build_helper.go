package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/benbjohnson/clock"
)

type RemoteBuildHelper struct {
	env                *environment.Environment
	imageHelper        *ImageHelper
	remoteBuildManager *containerregistry.RemoteBuildManager
	console            input.Console
	clock              clock.Clock
}

func NewRemoteBuildHelper(
	env *environment.Environment,
	imageHelper *ImageHelper,
	remoteBuildManager *containerregistry.RemoteBuildManager,
	console input.Console,
	clock clock.Clock,
) *RemoteBuildHelper {
	return &RemoteBuildHelper{
		env:                env,
		imageHelper:        imageHelper,
		remoteBuildManager: remoteBuildManager,
		console:            console,
		clock:              clock,
	}
}

func (rh *RemoteBuildHelper) RunRemoteBuild(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	target *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) error {
	dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

	if !filepath.IsAbs(dockerOptions.Path) {
		dockerOptions.Path = filepath.Join(serviceConfig.Path(), dockerOptions.Path)
	}

	if !filepath.IsAbs(dockerOptions.Context) {
		dockerOptions.Context = filepath.Join(serviceConfig.Path(), dockerOptions.Context)
	}

	if dockerOptions.Platform != "linux/amd64" {
		return fmt.Errorf("remote build only supports the linux/amd64 platform")
	}

	progress.SetProgress(NewServiceProgress("Packing remote build context"))

	contextPath, dockerPath, err := containerregistry.PackRemoteBuildSource(ctx, dockerOptions.Context, dockerOptions.Path)
	if contextPath != "" {
		defer os.Remove(contextPath)
	}
	if err != nil {
		return err
	}

	progress.SetProgress(NewServiceProgress("Uploading remote build context"))

	registryName, err := rh.imageHelper.RegistryName(ctx, serviceConfig)
	if err != nil {
		return err
	}

	registryResourceName := strings.Split(registryName, ".")[0]

	source, err := rh.remoteBuildManager.UploadBuildSource(
		ctx, target.SubscriptionId(), target.ResourceGroupName(), registryResourceName, contextPath)
	if err != nil {
		return err
	}

	localImageTag, err := rh.imageHelper.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return err
	}

	imageName, err := rh.imageHelper.RemoteImageTag(ctx, serviceConfig, localImageTag)
	if err != nil {
		return err
	}

	progress.SetProgress(NewServiceProgress("Running remote build"))

	buildRequest := &armcontainerregistry.DockerBuildRequest{
		SourceLocation: source.RelativePath,
		DockerFilePath: to.Ptr(dockerPath),
		IsPushEnabled:  to.Ptr(true),
		ImageNames:     []*string{to.Ptr(imageName)},
		Platform: &armcontainerregistry.PlatformProperties{
			OS:           to.Ptr(armcontainerregistry.OSLinux),
			Architecture: to.Ptr(armcontainerregistry.ArchitectureAmd64),
		},
	}

	previewerWriter := rh.console.ShowPreviewer(ctx,
		&input.ShowPreviewerOptions{
			Prefix:       "  ",
			MaxLineCount: 8,
			Title:        "Docker Output",
		})
	err = rh.remoteBuildManager.RunDockerBuildRequestWithLogs(
		ctx, target.SubscriptionId(), target.ResourceGroupName(), registryResourceName, buildRequest, previewerWriter)
	rh.console.StopPreviewer(ctx, false)
	if err != nil {
		return err
	}

	rh.env.SetServiceProperty(serviceConfig.Name, "IMAGE_NAME", fmt.Sprintf("%s/%s", registryName, imageName))
	return nil
}
