package azcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"golang.org/x/exp/slices"
)

// ContainerRegistryService provides access to query and login to Azure Container Registries (ACR)
type ContainerRegistryService interface {
	// Logs into the specified container registry
	LoginAcr(ctx context.Context, subscriptionId string, loginServer string) error
	// Gets a list of container registries for the specified subscription
	GetContainerRegistries(ctx context.Context, subscriptionId string) ([]*armcontainerregistry.Registry, error)
}

type containerRegistryService struct {
	userAgent  string
	httpClient httputil.HttpClient
	docker     docker.Docker
	credential azcore.TokenCredential
}

// Creates a new instance of the ContainerRegistryService
func NewContainerRegistryService(
	credential azcore.TokenCredential,
	httpClient httputil.HttpClient,
	docker docker.Docker,
) ContainerRegistryService {
	return &containerRegistryService{
		credential: credential,
		httpClient: httpClient,
		docker:     docker,
		userAgent:  azdinternal.MakeUserAgentString(""),
	}
}

// Gets a list of container registries for the specified subscription
func (crs *containerRegistryService) GetContainerRegistries(
	ctx context.Context,
	subscriptionId string,
) ([]*armcontainerregistry.Registry, error) {
	client, err := crs.createRegistriesClient(ctx, subscriptionId)
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

// Logs into the specified container registry
func (crs *containerRegistryService) LoginAcr(ctx context.Context, subscriptionId string, loginServer string,
) error {
	client, err := crs.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	parts := strings.Split(loginServer, ".")
	registryName := parts[0]

	// Find the registry and resource group
	_, resourceGroup, err := crs.findContainerRegistryByName(ctx, subscriptionId, registryName)
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
	err = crs.docker.Login(ctx, loginServer, username, *credResponse.Passwords[0].Value)
	if err != nil {
		return fmt.Errorf("failed logging into docker for username '%s' and server %s: %w", loginServer, username, err)
	}

	return nil
}

func (crs *containerRegistryService) findContainerRegistryByName(
	ctx context.Context,
	subscriptionId string,
	registryName string,
) (*armcontainerregistry.Registry, string, error) {
	registries, err := crs.GetContainerRegistries(ctx, subscriptionId)
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

func (crs *containerRegistryService) createRegistriesClient(
	ctx context.Context,
	subscriptionId string,
) (*armcontainerregistry.RegistriesClient, error) {
	options := clientOptionsBuilder(crs.httpClient, crs.userAgent).BuildArmClientOptions()
	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, crs.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating registries client: %w", err)
	}

	return client, nil
}
