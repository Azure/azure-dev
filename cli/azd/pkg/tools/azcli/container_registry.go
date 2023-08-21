package azcli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type dockerCredentials struct {
	Username    string
	Password    string
	LoginServer string
}

type acrToken struct {
	RefreshToken string `json:"refresh_token"`
}

// ContainerRegistryService provides access to query and login to Azure Container Registries (ACR)
type ContainerRegistryService interface {
	// Logs into the specified container registry
	Login(ctx context.Context, subscriptionId string, loginServer string) error
	// Gets a list of container registries for the specified subscription
	GetContainerRegistries(ctx context.Context, subscriptionId string) ([]*armcontainerregistry.Registry, error)
}

type containerRegistryService struct {
	credentialProvider account.SubscriptionCredentialProvider
	docker             docker.Docker
	httpClient         httputil.HttpClient
	userAgent          string
}

// Creates a new instance of the ContainerRegistryService
func NewContainerRegistryService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	docker docker.Docker,
) ContainerRegistryService {
	return &containerRegistryService{
		credentialProvider: credentialProvider,
		docker:             docker,
		httpClient:         httpClient,
		userAgent:          internal.UserAgent(),
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

func (crs *containerRegistryService) Login(ctx context.Context, subscriptionId string, loginServer string) error {
	// First attempt to get ACR credentials from the logged in user
	dockerCreds, tokenErr := crs.getTokenCredentials(ctx, subscriptionId, loginServer)
	if tokenErr != nil {
		log.Printf("failed getting ACR token credentials: %s\n", tokenErr.Error())

		// If that fails, attempt to get ACR credentials from the admin user
		adminCreds, adminErr := crs.getAdminUserCredentials(ctx, subscriptionId, loginServer)
		if adminErr != nil {
			return fmt.Errorf("failed logging into container registry, token: %w, admin: %w", tokenErr, adminErr)
		}

		dockerCreds = adminCreds
	}

	err := crs.docker.Login(ctx, dockerCreds.LoginServer, dockerCreds.Username, dockerCreds.Password)
	if err != nil {
		return fmt.Errorf(
			"failed logging into docker registry %s: %w",
			loginServer,
			err)
	}

	return nil
}

func (crs *containerRegistryService) getTokenCredentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*dockerCredentials, error) {
	acrToken, err := crs.getAcrToken(ctx, subscriptionId, loginServer)
	if err != nil {
		return nil, fmt.Errorf("failed getting ACR token: %w", err)
	}

	return &dockerCredentials{
		Username:    "00000000-0000-0000-0000-000000000000",
		Password:    acrToken.RefreshToken,
		LoginServer: loginServer,
	}, nil
}

// Logs into the specified container registry
func (crs *containerRegistryService) getAdminUserCredentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*dockerCredentials, error) {
	client, err := crs.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(loginServer, ".")
	registryName := parts[0]

	// Find the registry and resource group
	_, resourceGroup, err := crs.findContainerRegistryByName(ctx, subscriptionId, registryName)
	if err != nil {
		return nil, err
	}

	// Retrieve the registry credentials
	credResponse, err := client.ListCredentials(ctx, resourceGroup, registryName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container registry credentials: %w", err)
	}

	return &dockerCredentials{
		Username:    *credResponse.Username,
		Password:    *credResponse.Passwords[0].Value,
		LoginServer: loginServer,
	}, nil
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
	credential, err := crs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, crs.httpClient, crs.userAgent).BuildArmClientOptions()
	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating registries client: %w", err)
	}

	return client, nil
}

// Exchanges an AAD token for an ACR refresh token
func (crs *containerRegistryService) getAcrToken(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*acrToken, error) {
	creds, err := crs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting credentials for subscription '%s': %w", subscriptionId, err)
	}

	token, err := creds.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azure.ManagementScope}})
	if err != nil {
		return nil, fmt.Errorf("getting token for subscription '%s': %w", subscriptionId, err)
	}

	// Implementation based on docs @ https://azure.github.io/acr/AAD-OAuth.html
	options := clientOptionsBuilder(ctx, crs.httpClient, crs.userAgent).BuildCoreClientOptions()
	pipeline := azruntime.NewPipeline("azd-acr", internal.Version, azruntime.PipelineOptions{}, options)

	formData := url.Values{}
	formData.Set("grant_type", "access_token")
	formData.Set("service", loginServer)
	formData.Set("access_token", token.Token)

	tokenUrl := fmt.Sprintf("https://%s/oauth2/exchange", loginServer)
	req, err := azruntime.NewRequest(ctx, http.MethodPost, tokenUrl)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	setHttpRequestBody(req, formData)

	response, err := pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !azruntime.HasStatusCode(response, http.StatusOK) {
		return nil, azruntime.NewResponseError(response)
	}

	acrTokenBody, err := httputil.ReadRawResponse[acrToken](response)
	if err != nil {
		return nil, err
	}

	return acrTokenBody, nil
}

func setHttpRequestBody(req *policy.Request, formData url.Values) {
	raw := req.Raw()
	raw.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	raw.Body = io.NopCloser(strings.NewReader(formData.Encode()))
}
