package azcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"golang.org/x/exp/slices"
)

func (cli *azCli) GetContainerRegistries(
	ctx context.Context,
	subscriptionId string,
) ([]*armcontainerregistry.Registry, error) {
	client, err := cli.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	results := []*armcontainerregistry.Registry{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of registries: %w", err)
		}

		results = append(results, page.RegistryListResult.Value...)
	}

	return results, nil
}

func (cli *azCli) LoginAcr(ctx context.Context,
	commandRunner exec.CommandRunner, subscriptionId string, loginServer string,
) error {
	client, err := cli.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	parts := strings.Split(loginServer, ".")
	registryName := parts[0]

	// Find the registry and resource group
	_, resourceGroup, err := cli.findContainerRegistryByName(ctx, subscriptionId, registryName)
	if err != nil {
		return err
	}

	// Retrieve the registry credentials
	credResponse, err := client.ListCredentials(ctx, resourceGroup, registryName, nil)
	if err != nil {
		return fmt.Errorf("getting container registry credentials: %w", err)
	}

	username := *credResponse.Username

	// Login to docker with ACR credentials to allow push operations
	dockerCli := docker.NewDocker(commandRunner)
	err = dockerCli.Login(ctx, loginServer, username, *credResponse.Passwords[0].Value)
	if err != nil {
		return fmt.Errorf("failed logging into docker for username '%s' and server %s: %w", loginServer, username, err)
	}

	return nil
}

func (cli *azCli) findContainerRegistryByName(
	ctx context.Context,
	subscriptionId string,
	registryName string,
) (*armcontainerregistry.Registry, string, error) {
	registries, err := cli.GetContainerRegistries(ctx, subscriptionId)
	if err != nil {
		return nil, "", fmt.Errorf("failed listing container registries: %w", err)
	}

	matchIndex := slices.IndexFunc(registries, func(registry *armcontainerregistry.Registry) bool {
		return *registry.Name == registryName
	})

	if matchIndex == -1 {
		return nil, "", fmt.Errorf(
			"cannot find registry with name '%s' and subscriptionId '%s'",
			registryName,
			subscriptionId,
		)
	}

	registry := registries[matchIndex]
	resourceGroup := azure.GetResourceGroupName(*registry.ID)
	if resourceGroup == nil {
		return nil, "", fmt.Errorf("cannot find resource group for resource id: '%s'", *registry.ID)
	}

	return registry, *resourceGroup, nil
}

func (cli *azCli) createRegistriesClient(
	ctx context.Context,
	subscriptionId string,
) (*armcontainerregistry.RegistriesClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating registries client: %w", err)
	}

	return client, nil
}
