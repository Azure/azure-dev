package project

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
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
	config           *ServiceConfig
	env              *environment.Environment
	scope            *environment.TargetResource
	containerService azcli.ContainerServiceClient
	az               azcli.AzCli
	docker           docker.Docker
	kubectl          kubectl.KubectlCli
}

func (t *aksTarget) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{t.docker}
}

func (t *aksTarget) Deploy(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	path string,
	progress chan<- string,
) (ServiceDeploymentResult, error) {
	// Login to AKS cluster
	namespace := t.config.Project.Name
	clusterName, has := t.env.Values[environment.AksClusterEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf(
			"could not determine AKS cluster, ensure %s is set as an output of your infrastructure",
			environment.AksClusterEnvVarName,
		)
	}

	log.Printf("getting AKS credentials %s\n", clusterName)
	progress <- "Getting AKS credentials"
	credentials, err := t.containerService.GetAdminCredentials(ctx, t.scope.ResourceGroupName(), clusterName)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	kubeConfigManager, err := kubectl.NewKubeConfigManager(t.kubectl)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	kubeConfig, err := kubectl.ParseKubeConfig(ctx, credentials.Kubeconfigs[0].Value)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed parsing kube config: %w", err)
	}

	if err := kubeConfigManager.SaveKubeConfig(ctx, clusterName, kubeConfig); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed saving kube config: %w", err)
	}

	if err := kubeConfigManager.MergeConfigs(ctx, "config", "config", clusterName); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed merging kube configs: %w", err)
	}

	if _, err := t.kubectl.ConfigUseContext(ctx, clusterName, nil); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed using kube context '%s', %w", clusterName, err)
	}

	kubeFlags := kubectl.KubeCliFlags{
		Namespace: namespace,
		DryRun:    "client",
		Output:    "yaml",
	}

	progress <- "Creating k8s namespace"
	namespaceResult, err := t.kubectl.CreateNamespace(
		ctx,
		namespace,
		&kubectl.KubeCliFlags{DryRun: "client", Output: "yaml"},
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed creating kube namespace: %w", err)
	}

	_, err = t.kubectl.ApplyPipe(ctx, namespaceResult.Stdout, nil)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed applying kube namespace: %w", err)
	}

	progress <- "Creating k8s secrets"
	secretResult, err := t.kubectl.CreateSecretGenericFromLiterals(ctx, "azd", t.env.Environ(), &kubeFlags)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed setting kube secrets: %w", err)
	}

	_, err = t.kubectl.ApplyPipe(ctx, secretResult.Stdout, nil)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed applying kube secrets: %w", err)
	}

	// Login to container registry.
	loginServer, has := t.env.Values[environment.ContainerRegistryEndpointEnvVarName]
	if !has {
		return ServiceDeploymentResult{}, fmt.Errorf(
			"could not determine container registry endpoint, ensure %s is set as an output of your infrastructure",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	log.Printf("logging into registry %s\n", loginServer)

	progress <- "Logging into container registry"
	if err := t.az.LoginAcr(ctx, t.docker, t.env.GetSubscriptionId(), loginServer); err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("logging into registry '%s': %w", loginServer, err)
	}

	resourceName := t.scope.ResourceName()
	if resourceName == "" {
		resourceName = t.config.Name
	}

	tags := []string{
		fmt.Sprintf("%s/%s/%s:azdev-deploy-%d", loginServer, t.config.Project.Name, resourceName, time.Now().Unix()),
		fmt.Sprintf("%s/%s/%s:latest", loginServer, t.config.Project.Name, resourceName),
	}

	for _, tag := range tags {
		// Tag image.
		log.Printf("tagging image %s as %s", path, tag)
		progress <- "Tagging image"
		if err := t.docker.Tag(ctx, t.config.Path(), path, tag); err != nil {
			return ServiceDeploymentResult{}, fmt.Errorf("tagging image: %w", err)
		}

		// Push image.
		progress <- "Pushing container image"
		if err := t.docker.Push(ctx, t.config.Path(), tag); err != nil {
			return ServiceDeploymentResult{}, fmt.Errorf("pushing image: %w", err)
		}
	}

	progress <- "Applying k8s manifests"
	t.kubectl.SetEnv(t.env.Values)
	err = t.kubectl.ApplyFiles(
		ctx,
		filepath.Join(t.config.RelativePath, "manifests"),
		&kubectl.KubeCliFlags{Namespace: namespace},
	)
	if err != nil {
		return ServiceDeploymentResult{}, fmt.Errorf("failed applying kube manifests: %w", err)
	}

	endpoints, err := t.Endpoints(ctx)
	if err != nil {
		return ServiceDeploymentResult{}, err
	}

	return ServiceDeploymentResult{
		TargetResourceId: azure.ContainerAppRID(
			t.env.GetSubscriptionId(),
			t.scope.ResourceGroupName(),
			t.scope.ResourceName(),
		),
		Kind:      ContainerAppTarget,
		Details:   nil,
		Endpoints: endpoints,
	}, nil
}

func (t *aksTarget) Endpoints(ctx context.Context) ([]string, error) {
	// TODO Update
	return []string{"https://aks.azure.com/sample"}, nil
}

func NewAksTarget(
	config *ServiceConfig,
	env *environment.Environment,
	scope *environment.TargetResource,
	azCli azcli.AzCli,
	containerService azcli.ContainerServiceClient,
	kubectlCli kubectl.KubectlCli,
	docker docker.Docker,
) ServiceTarget {
	return &aksTarget{
		config:           config,
		env:              env,
		scope:            scope,
		az:               azCli,
		containerService: containerService,
		docker:           docker,
		kubectl:          kubectlCli,
	}
}
