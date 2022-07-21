// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type containerAppTarget struct {
	config *ServiceConfig
	env    *environment.Environment
	scope  *environment.DeploymentScope
	cli    tools.AzCli
	docker *tools.Docker
}

func (at *containerAppTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{at.cli, at.docker}
}

func (at *containerAppTarget) Deploy(ctx context.Context, azdCtx *environment.AzdContext, path string, progress chan<- string) (ServiceDeploymentResult, error) {
	// Login to container registry.
	loginServer, has := at.env.Values[environment.ContainerRegistryEndpointEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf("could not determine container registry endpoint, ensure %s is set as an output of your infrastructure", environment.ContainerRegistryEndpointEnvVarName)
	}

	log.Printf("logging into registry %s", loginServer)

	progress <- "Logging into container registry"
	if err := at.cli.LoginAcr(ctx, at.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	fullTag := fmt.Sprintf("%s/%s/%s:azdev-deploy-%d", loginServer, at.scope.ResourceName(), at.scope.ResourceName(), time.Now().Unix())

	// Tag image.
	log.Printf("tagging image %s as %s", path, fullTag)
	progress <- "Tagging image"
	if err := at.docker.Tag(ctx, at.config.Path(), path, fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("tagging image: %w", err)
	}

	log.Printf("pushing %s to registry", fullTag)

	// Push image.
	progress <- "Pushing container image"
	if err := at.docker.Push(ctx, at.config.Path(), fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("pushing image: %w", err)
	}

	log.Printf("writing image name to environment")

	// Save the name of the image we pushed into the environment with a well known key.
	at.env.Values[fmt.Sprintf("SERVICE_%s_IMAGE_NAME", strings.ToUpper(at.config.Name))] = fullTag

	if err := at.env.Save(); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("saving image name to environment: %w", err)
	}

	if strings.TrimSpace(at.config.Infra.Module) == "" {
		at.config.Infra.Module = at.config.Name
	}

	infraProvider, err := provisioning.NewInfraProvider(at.env, at.config.Project.Path, at.config.Infra, at.cli)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("creating infrastructure provider: %w", err)
	}

	progress <- "Creating deployment template"
	template, err := infraProvider.Plan(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("compiling template: %w", err)
	}

	progress <- "Updating container app image reference"
	scope := provisioning.NewResourceGroupProvisioningScope(at.cli, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.env.GetEnvName())
	deployChannel, progressChannel := infraProvider.Apply(ctx, template, scope)

	go func() {
		for progressReport := range progressChannel {
			progress <- createProgressMessage(progressReport)
		}
	}()

	deployResult := <-deployChannel
	if deployResult.Error != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("updating infrastructure: %w", err)
	}

	progress <- "Updating environment"
	provisioning.UpdateEnvironment(at.env, &deployResult.Outputs)

	progress <- "Fetching endpoints for container app service"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.ContainerAppRID(at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName()),
		Kind:             ContainerAppTarget,
		Details:          deployResult,
		Endpoints:        endpoints,
	}, nil
}

func (at *containerAppTarget) Endpoints(ctx context.Context) ([]string, error) {
	containerAppProperties, err := at.cli.GetContainerAppProperties(ctx, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	return []string{fmt.Sprintf("https://%s/", containerAppProperties.Properties.Configuration.Ingress.Fqdn)}, nil
}

func createProgressMessage(progressReport *provisioning.ProvisionApplyProgress) string {
	succeededCount := 0

	for _, resourceOperation := range progressReport.Operations {
		if resourceOperation.Properties.ProvisioningState == "Succeeded" {
			succeededCount++
		}
	}

	return fmt.Sprintf("Creating Azure resources (%d of ~%d completed) ", succeededCount, len(progressReport.Operations))
}

func NewContainerAppTarget(config *ServiceConfig, env *environment.Environment, scope *environment.DeploymentScope, azCli tools.AzCli, docker *tools.Docker) ServiceTarget {
	return &containerAppTarget{
		config: config,
		env:    env,
		scope:  scope,
		cli:    azCli,
		docker: docker,
	}
}
