// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/iac/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/drone/envsubst"
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
	bicepPath := azdCtx.BicepModulePath(at.config.ModuleName)

	progress <- "Creating deployment template"
	template, err := bicep.Compile(ctx, tools.NewBicepCli(at.cli), bicepPath)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

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

	log.Print("generating deployment parameters file")

	// Copy the parameter template file to the environment working directory and do substitutions.
	parametersTemplate := azdCtx.BicepParametersTemplateFilePath(at.config.ModuleName)
	templateBytes, err := ioutil.ReadFile(parametersTemplate)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("reading parameter file template: %w", err)
	}

	replaced, err := envsubst.Eval(string(templateBytes), func(name string) string {
		if val, has := at.env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("substituting parameter file: %w", err)
	}

	parametersFile := azdCtx.BicepParametersFilePath(at.env.GetEnvName(), at.config.ModuleName)

	// If the bicep uses nested modules ensure the full directory tree
	// is created before copying the parameters file.
	directoryPath := filepath.Dir(parametersFile)
	if err := os.MkdirAll(directoryPath, 0750); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("creating directory tree: %w", err)
	}

	err = ioutil.WriteFile(parametersFile, []byte(replaced), 0644)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("writing parameter file: %w", err)
	}
	log.Printf("generated deployment parameters file %s", parametersFile)

	log.Printf("running ARM deployment to update container")
	deploymentTarget := bicep.NewResourceGroupDeploymentTarget(at.cli, at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName())

	progress <- "Updating container app image reference"
	res, err := bicep.Deploy(ctx, deploymentTarget, azdCtx.BicepModulePath(at.config.ModuleName), parametersFile)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("updating infrastructure: %w", err)
	}

	if len(res.Properties.Outputs) > 0 {
		log.Printf("saving %d deployment outputs", len(res.Properties.Outputs))

		template.CanonicalizeDeploymentOutputs(&res.Properties.Outputs)

		for name, o := range res.Properties.Outputs {
			at.env.Values[name] = fmt.Sprintf("%v", o.Value)
		}

		if err := at.env.Save(); err != nil {
			return ServiceDeploymentResult{}, fmt.Errorf("saving outputs to environment: %w", err)
		}
	}

	progress <- "Fetching endpoints for container app service"
	endpoints, err := at.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.ContainerAppRID(at.env.GetSubscriptionId(), at.scope.ResourceGroupName(), at.scope.ResourceName()),
		Kind:             ContainerAppTarget,
		Details:          res,
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

func NewContainerAppTarget(config *ServiceConfig, env *environment.Environment, scope *environment.DeploymentScope, azCli tools.AzCli, docker *tools.Docker) ServiceTarget {
	return &containerAppTarget{
		config: config,
		env:    env,
		scope:  scope,
		cli:    azCli,
		docker: docker,
	}
}
