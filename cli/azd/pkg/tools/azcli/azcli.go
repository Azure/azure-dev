// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

var (
	ErrAzCliNotLoggedIn         = errors.New("cli is not logged in. Try running \"azd login\" to fix")
	ErrAzCliRefreshTokenExpired = errors.New("refresh token has expired. Try running \"azd login\" to fix")
	ErrClientAssertionExpired   = errors.New("client assertion expired")
	ErrDeploymentNotFound       = errors.New("deployment not found")
	ErrNoConfigurationValue     = errors.New("no value configured")
	ErrAzCliSecretNotFound      = errors.New("secret not found")
)

type AzCli interface {
	// SetUserAgent sets the user agent that's sent with each call to the Azure
	// CLI via the `AZURE_HTTP_USER_AGENT` environment variable.
	SetUserAgent(userAgent string)

	// UserAgent gets the currently configured user agent
	UserAgent() string

	LoginAcr(ctx context.Context, commandRunner exec.CommandRunner, subscriptionId string, loginServer string) error
	GetContainerRegistries(ctx context.Context, subscriptionId string) ([]*armcontainerregistry.Registry, error)
	ListAccounts(ctx context.Context) ([]*AzCliSubscriptionInfo, error)
	GetAccount(ctx context.Context, subscriptionId string) (*AzCliSubscriptionInfo, error)
	GetSubscriptionDeployment(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) (*armresources.DeploymentExtended, error)
	GetResourceGroupDeployment(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) (*armresources.DeploymentExtended, error)
	GetResource(ctx context.Context, subscriptionId string, resourceId string) (AzCliResourceExtended, error)
	GetKeyVault(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		vaultName string,
	) (*AzCliKeyVault, error)
	GetKeyVaultSecret(ctx context.Context, vaultName string, secretName string) (*AzCliKeyVaultSecret, error)
	PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string, location string) error
	GetAppConfig(
		ctx context.Context, subscriptionId string, resourceGroupName string, configName string) (*AzCliAppConfig, error)
	PurgeAppConfig(ctx context.Context, subscriptionId string, configName string, location string) error
	GetApim(
		ctx context.Context, subscriptionId string, resourceGroupName string, apimName string) (*AzCliApim, error)
	PurgeApim(ctx context.Context, subscriptionId string, apimName string, location string) error
	DeployAppServiceZip(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		appName string,
		deployZipFile io.Reader,
	) (*string, error)
	DeployFunctionAppUsingZipFile(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
		deployZipFile io.Reader,
	) (*string, error)
	GetFunctionAppProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
	) (*AzCliFunctionAppProperties, error)
	DeployToSubscription(
		ctx context.Context, subscriptionId, deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
		location string) (
		AzCliDeploymentResult, error)
	DeployToResourceGroup(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		deploymentName string,
		armTemplate azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (AzCliDeploymentResult, error)
	DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error
	DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error
	ListResourceGroup(
		ctx context.Context,
		subscriptionId string,
		listOptions *ListResourceGroupOptions,
	) ([]AzCliResource, error)
	ListResourceGroupResources(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		listOptions *ListResourceGroupResourcesOptions,
	) ([]AzCliResource, error)
	ListSubscriptionDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ListResourceGroupDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	// ListAccountLocations lists the physical locations in Azure.
	ListAccountLocations(ctx context.Context, subscriptionId string) ([]AzCliLocation, error)
	// CreateOrUpdateServicePrincipal creates a service principal using a given name and returns a JSON object which
	// may be used by tools which understand the `AZURE_CREDENTIALS` format (i.e. the `sdk-auth` format). The service
	// principal is assigned a given role. If an existing principal exists with the given name,
	// it is updated in place and its credentials are reset.
	CreateOrUpdateServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		applicationName string,
		roleToAssign string,
	) (json.RawMessage, error)
	GetAppServiceProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		applicationName string,
	) (*AzCliAppServiceProperties, error)
	GetContainerAppProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		applicationName string,
	) (*AzCliContainerAppProperties, error)
	GetStaticWebAppProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		appName string,
	) (*AzCliStaticWebAppProperties, error)
	GetStaticWebAppApiKey(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (*string, error)
	GetStaticWebAppEnvironmentProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		appName string,
		environmentName string,
	) (*AzCliStaticWebAppEnvironmentProperties, error)

	GetSignedInUserId(ctx context.Context) (*string, error)

	GetAccessToken(ctx context.Context) (*AzCliAccessToken, error)
}

type AzCliDeployment struct {
	Id         string                    `json:"id"`
	Name       string                    `json:"name"`
	Properties AzCliDeploymentProperties `json:"properties"`
}

type AzCliDeploymentProperties struct {
	CorrelationId   string                                `json:"correlationId"`
	Error           AzCliDeploymentErrorResponse          `json:"error"`
	Dependencies    []AzCliDeploymentPropertiesDependency `json:"dependencies"`
	OutputResources []AzCliDeploymentResourceReference    `json:"outputResources"`
	Outputs         map[string]AzCliDeploymentOutput      `json:"outputs"`
}

type AzCliDeploymentPropertiesDependency struct {
	AzCliDeploymentPropertiesBasicDependency
	DependsOn []AzCliDeploymentPropertiesBasicDependency `json:"dependsOn"`
}

type AzCliDeploymentPropertiesBasicDependency struct {
	Id           string `json:"id"`
	ResourceName string `json:"resourceName"`
	ResourceType string `json:"resourceType"`
}

type AzCliDeploymentResult struct {
	Properties AzCliDeploymentResultProperties `json:"properties"`
}

type AzCliDeploymentResultProperties struct {
	Outputs map[string]AzCliDeploymentOutput `json:"outputs"`
}

type AzCliDeploymentErrorResponse struct {
	Code           string                         `json:"code"`
	Message        string                         `json:"message"`
	Target         string                         `json:"target"`
	Details        []AzCliDeploymentErrorResponse `json:"details"`
	AdditionalInfo AzCliDeploymentAdditionalInfo  `json:"additionalInfo"`
}

type AzCliDeploymentAdditionalInfo struct {
	Type string      `json:"type"`
	Info interface{} `json:"info"`
}

type AzCliDeploymentOutput struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

type AzCliResource struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
}

type AzCliResourceExtended struct {
	AzCliResource
	Kind string `json:"kind"`
}

type AzCliDeploymentResourceReference struct {
	Id string `json:"id"`
}

type AzCliResourceOperation struct {
	Id          string                           `json:"id"`
	OperationId string                           `json:"operationId"`
	Properties  AzCliResourceOperationProperties `json:"properties"`
}

type AzCliResourceOperationProperties struct {
	ProvisioningOperation string                               `json:"provisioningOperation"`
	ProvisioningState     string                               `json:"provisioningState"`
	TargetResource        AzCliResourceOperationTargetResource `json:"targetResource"`
	StatusCode            string                               `json:"statusCode"`
	StatusMessage         AzCliDeploymentStatusMessage         `json:"statusMessage"`
	// While the operation is in progress, this timestamp effectively represents "InProgressTimestamp".
	// When the operation ends, this timestamp effectively represents "EndTimestamp".
	Timestamp time.Time `json:"timestamp"`
}

type AzCliDeploymentStatusMessage struct {
	Err    AzCliDeploymentErrorResponse `json:"error"`
	Status string                       `json:"status"`
}

type AzCliResourceOperationTargetResource struct {
	Id            string `json:"id"`
	ResourceType  string `json:"resourceType"`
	ResourceName  string `json:"resourceName"`
	ResourceGroup string `json:"resourceGroup"`
}

// AzCliConfigValue represents the value returned by `az config get`.
type AzCliConfigValue struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// AzCliConfigValue represents the value in the array returned by `az extension list`.
type AzCliExtensionInfo struct {
	Name string
}

// Optional parameters for resource group listing.
type ListResourceGroupOptions struct {
	// An optional tag filter
	TagFilter *Filter
	// An optional filter expression to filter the resource group results
	// https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/list
	Filter *string
}

// Optional parameters for resource group resources listing.
type ListResourceGroupResourcesOptions struct {
	// An optional filter expression to filter the resource list result
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	Filter *string
}

type Filter struct {
	Key   string
	Value string
}

type NewAzCliArgs struct {
	EnableDebug     bool
	EnableTelemetry bool
	HttpClient      httputil.HttpClient
}

func NewAzCli(credential azcore.TokenCredential, args NewAzCliArgs) AzCli {
	return &azCli{
		userAgent:       azdinternal.MakeUserAgentString(""),
		enableDebug:     args.EnableDebug,
		enableTelemetry: args.EnableTelemetry,
		httpClient:      args.HttpClient,
		credential:      credential,
	}
}

type azCli struct {
	userAgent       string
	enableDebug     bool
	enableTelemetry bool

	// Allows us to mock the Http Requests from the go modules
	httpClient httputil.HttpClient

	credential azcore.TokenCredential
}

// SetUserAgent sets the user agent that's sent with each call to the Azure
// CLI via the `AZURE_HTTP_USER_AGENT` environment variable.
func (cli *azCli) SetUserAgent(userAgent string) {
	cli.userAgent = userAgent
}

func (cli *azCli) UserAgent() string {
	return cli.userAgent
}

func (cli *azCli) createDefaultClientOptionsBuilder(ctx context.Context) *azsdk.ClientOptionsBuilder {
	return azsdk.NewClientOptionsBuilder().
		WithTransport(httputil.GetHttpClient(ctx)).
		WithPerCallPolicy(azsdk.NewUserAgentPolicy(cli.UserAgent()))
}
