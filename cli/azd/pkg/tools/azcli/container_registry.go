package azcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

func (cli *azCli) LoginAcr(ctx context.Context, subscriptionId string, resourceGroup, loginServer string) error {
	client, err := cli.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	parts := strings.Split(loginServer, ".")
	registryName := parts[0]

	// Retrieve the registry credentials
	credResponse, err := client.ListCredentials(ctx, resourceGroup, registryName, nil)
	if err != nil {
		return fmt.Errorf("getting container registry credentials: %w", err)
	}

	username := *credResponse.Username

	// Login to docker with ACR credentials to allow push operations
	dockerCli := docker.NewDocker(ctx)
	err = dockerCli.Login(ctx, loginServer, username, *credResponse.Passwords[0].Value)
	if err != nil {
		return fmt.Errorf("failed logging into docker for username '%s' and server %s: %w", loginServer, username, err)
	}

	return nil
}

func (cli *azCli) createRegistriesClient(ctx context.Context, subscriptionId string) (*armcontainerregistry.RegistriesClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createClientOptions(ctx)
	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating registries client: %w", err)
	}

	return client, nil
}
