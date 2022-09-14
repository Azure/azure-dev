package project

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
)

type aksTarget struct {
	config  *ServiceConfig
	env     *environment.Environment
	scope   *environment.DeploymentScope
	az      azcli.AzCli
	docker  docker.Docker
	kubectl kubectl.KubectlCli
}

func (t *aksTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{t.az, t.docker}
}

func (t *aksTarget) Deploy(ctx context.Context, azdCtx *azdcontext.AzdContext, path string, progress chan<- string) (ServiceDeploymentResult, error) {
	// Login to AKS cluster
	clusterName, has := t.env.Values[environment.AksClusterEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf("could not determine AKS cluster, ensure %s is set as an output of your infrastructure", environment.AksClusterEnvVarName)
	}

	log.Printf("getting AKS credentials %s\n", clusterName)
	progress <- "Getting AKS credentials"
	if err := t.az.GetAksCredentials(ctx, t.scope.ResourceGroupName(), clusterName); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("getting AKS credentials for '%s': %w", clusterName, err)
	}

	_, err := t.kubectl.GetNodes(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("getting AKS nodes: %w", err)
	}

	// Login to container registry.
	loginServer, has := t.env.Values[environment.ContainerRegistryEndpointEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf("could not determine container registry endpoint, ensure %s is set as an output of your infrastructure", environment.ContainerRegistryEndpointEnvVarName)
	}

	log.Printf("logging into registry %s\n", loginServer)

	progress <- "Logging into container registry"
	if err := t.az.LoginAcr(ctx, t.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	fullTag := fmt.Sprintf("%s/%s/%s:azdev-deploy-%d", loginServer, t.scope.ResourceName(), t.scope.ResourceName(), time.Now().Unix())

	// Tag image.
	log.Printf("tagging image %s as %s", path, fullTag)
	progress <- "Tagging image"
	if err := t.docker.Tag(ctx, t.config.Path(), path, fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("tagging image: %w", err)
	}

	log.Printf("pushing %s to registry", fullTag)

	// Push image.
	progress <- "Pushing container image"
	if err := t.docker.Push(ctx, t.config.Path(), fullTag); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("pushing image: %w", err)
	}

	endpoints, err := t.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.ContainerAppRID(t.env.GetSubscriptionId(), t.scope.ResourceGroupName(), t.scope.ResourceName()),
		Kind:             ContainerAppTarget,
		Details:          nil,
		Endpoints:        endpoints,
	}, nil
}

func (t *aksTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO Update
	return []string{"https://aks.azure.com/sample"}, nil
	// containerAppProperties, err := t.cli.GetContainerAppProperties(ctx, t.env.GetSubscriptionId(), t.scope.ResourceGroupName(), t.scope.ResourceName())
	// if err != nil {
	// 	return nil, fmt.Errorf("fetching service properties: %w", err)
	// }

	// return []string{fmt.Sprintf("https://%s/", containerAppProperties.Properties.Configuration.Ingress.Fqdn)}, nil
}

func NewAksTarget(config *ServiceConfig, env *environment.Environment, scope *environment.DeploymentScope, azCli azcli.AzCli, kubectlCli kubectl.KubectlCli, docker docker.Docker) ServiceTarget {
	return &aksTarget{
		config:  config,
		env:     env,
		scope:   scope,
		az:      azCli,
		docker:  docker,
		kubectl: kubectlCli,
	}
}
