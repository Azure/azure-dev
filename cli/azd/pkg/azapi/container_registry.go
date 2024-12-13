package azapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

// Credentials for authenticating with a docker registry,
// can be both username/password or access token based.
type DockerCredentials struct {
	// Username is the name of the user. Note that this may be set to a value like
	// '00000000-0000-0000-0000-000000000000' when using access tokens.
	Username string
	// Password is the password for the user, or the access token when using access tokens.
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
	// Gets the credentials that could be used to login to the specified container registry.
	Credentials(ctx context.Context, subscriptionId string, loginServer string) (*DockerCredentials, error)
	// Gets a list of container registries for the specified subscription
	GetContainerRegistries(ctx context.Context, subscriptionId string) ([]*armcontainerregistry.Registry, error)
}

type containerRegistryService struct {
	credentialProvider account.SubscriptionCredentialProvider
	docker             *docker.Cli
	armClientOptions   *arm.ClientOptions
	coreClientOptions  *azcore.ClientOptions
}

// Creates a new instance of the ContainerRegistryService
func NewContainerRegistryService(
	credentialProvider account.SubscriptionCredentialProvider,
	docker *docker.Cli,
	armClientOptions *arm.ClientOptions,
	coreClientOptions *azcore.ClientOptions,
) ContainerRegistryService {
	return &containerRegistryService{
		credentialProvider: credentialProvider,
		docker:             docker,
		armClientOptions:   armClientOptions,
		coreClientOptions:  coreClientOptions,
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
	dockerCreds, err := crs.Credentials(ctx, subscriptionId, loginServer)
	if err != nil {
		return err
	}

	err = crs.docker.Login(ctx, dockerCreds.LoginServer, dockerCreds.Username, dockerCreds.Password)
	if err != nil {
		return fmt.Errorf(
			"failed logging into docker registry %s: %w",
			loginServer,
			err)
	}

	return nil
}

// Credentials gets the credentials that could be used to login to the specified container registry. It prefers to use
// AAD token credentials for the current user, but if that fails it will fall back to admin user credentials.
// Note: the loginServer returned as part of the credentials, and will always match the parameter on success, and is
// only added as convenience.
func (crs *containerRegistryService) Credentials(
	ctx context.Context, subscriptionId string, loginServer string,
) (*DockerCredentials, error) {
	// First attempt to get ACR credentials from the logged in user
	dockerCreds, tokenErr := crs.getTokenCredentials(ctx, subscriptionId, loginServer)
	if tokenErr != nil {
		var httpErr *azcore.ResponseError
		if errors.As(tokenErr, &httpErr) {
			if httpErr.StatusCode == 404 {
				// No need to try admin user credentials if getToken returns 404. It means the registry was not found.
				return nil, tokenErr
			}
		}

		log.Printf("failed getting ACR token credentials: %s\n", tokenErr.Error())
		// If that fails, attempt to get ACR credentials from the admin user
		adminCreds, adminErr := crs.getAdminUserCredentials(ctx, subscriptionId, loginServer)
		if adminErr != nil {
			return nil, fmt.Errorf("failed logging into container registry, token: %w, admin: %w", tokenErr, adminErr)
		}

		dockerCreds = adminCreds
	}

	return dockerCreds, nil
}

// getTokenCredentials
func (crs *containerRegistryService) getTokenCredentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*DockerCredentials, error) {
	acrToken, err := crs.getAcrToken(ctx, subscriptionId, loginServer)
	if err != nil {
		return nil, fmt.Errorf("failed getting ACR token: %w", err)
	}

	// Set it to 00000000-0000-0000-0000-000000000000 as per documented in
	//nolint:lll
	// https://learn.microsoft.com/azure/container-registry/container-registry-authentication?tabs=azure-cli#individual-login-with-microsoft-entra-id
	return &DockerCredentials{
		Username:    "00000000-0000-0000-0000-000000000000",
		Password:    acrToken.RefreshToken,
		LoginServer: loginServer,
	}, nil
}

// getAdminUserCredentials gets the credentials for the admin user of the specified container registry or an error if
// the registry does not have the admin user enabled.
func (crs *containerRegistryService) getAdminUserCredentials(
	ctx context.Context,
	subscriptionId string,
	loginServer string,
) (*DockerCredentials, error) {
	client, err := crs.createRegistriesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(loginServer, ".")
	registryName := parts[0]

	// Find the registry and resource group
	registry, resourceGroup, err := crs.findContainerRegistryByName(ctx, subscriptionId, registryName)
	if err != nil {
		return nil, err
	}

	// Ensure the registry has admin user enabled
	if registry.Properties.AdminUserEnabled == nil || !*registry.Properties.AdminUserEnabled {
		return nil, fmt.Errorf("admin user is not enabled for container registry '%s'", registryName)
	}

	// Retrieve the registry credentials
	credResponse, err := client.ListCredentials(ctx, resourceGroup, registryName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container registry credentials: %w", err)
	}

	return &DockerCredentials{
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

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, credential, crs.armClientOptions)
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

	token, err := creds.GetToken(
		ctx,
		policy.TokenRequestOptions{Scopes: []string{
			fmt.Sprintf("%s//.default", crs.armClientOptions.Cloud.Services[cloud.ResourceManager].Endpoint),
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("getting token for subscription '%s': %w", subscriptionId, err)
	}

	// Implementation based on docs @ https://azure.github.io/acr/AAD-OAuth.html
	pipeline := azruntime.NewPipeline("azd-acr", internal.Version, azruntime.PipelineOptions{}, crs.coreClientOptions)

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
