// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"errors"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

var (
	ErrAzCliNotLoggedIn         = errors.New("cli is not logged in. Try running \"az login\" to fix")
	ErrAzCliRefreshTokenExpired = errors.New("refresh token has expired. Try running \"az login\" to fix")
	ErrClientAssertionExpired   = errors.New("client assertion expired")
	ErrNoConfigurationValue     = errors.New("no value configured")
)

type AzCli interface {
	GetResource(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		apiVersion string,
	) (AzCliResourceExtended, error)
	GetManagedHSM(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		hsmName string,
	) (*AzCliManagedHSM, error)
	GetCognitiveAccount(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		accountName string,
	) (armcognitiveservices.Account, error)
	GetAppConfig(
		ctx context.Context, subscriptionId string, resourceGroupName string, configName string) (*AzCliAppConfig, error)
	PurgeApim(ctx context.Context, subscriptionId string, apimName string, location string) error
	PurgeAppConfig(ctx context.Context, subscriptionId string, configName string, location string) error
	PurgeManagedHSM(ctx context.Context, subscriptionId string, hsmName string, location string) error
	PurgeCognitiveAccount(ctx context.Context, subscriptionId, location, resourceGroup, accountName string) error
	GetApim(
		ctx context.Context, subscriptionId string, resourceGroupName string, apimName string) (*AzCliApim, error)
	DeployAppServiceZip(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		appName string,
		deployZipFile io.ReadSeeker,
		logProgress func(string),
	) (*string, error)
	DeployFunctionAppUsingZipFile(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
		deployZipFile io.ReadSeeker,
		remoteBuild bool,
	) (*string, error)
	GetFunctionAppProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
	) (*AzCliFunctionAppProperties, error)

	DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error
	CreateOrUpdateResourceGroup(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		location string,
		tags map[string]*string,
	) error
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
	// CreateOrUpdateServicePrincipal creates a service principal using a given name and returns a JSON object which
	// may be used by tools which understand the `AZURE_CREDENTIALS` format (i.e. the `sdk-auth` format). The service
	// principal is assigned a given role. If an existing principal exists with the given name,
	// it is updated in place and its credentials are reset.
	GetAppServiceProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		applicationName string,
	) (*AzCliAppServiceProperties, error)
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
}

func NewAzCli(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	args NewAzCliArgs,
	armClientOptions *arm.ClientOptions,
) AzCli {
	return &azCli{
		credentialProvider: credentialProvider,
		enableDebug:        args.EnableDebug,
		enableTelemetry:    args.EnableTelemetry,
		httpClient:         httpClient,
		armClientOptions:   armClientOptions,
	}
}

type azCli struct {
	enableDebug     bool
	enableTelemetry bool

	// Allows us to mock the Http Requests from the go modules
	httpClient httputil.HttpClient

	credentialProvider account.SubscriptionCredentialProvider

	armClientOptions *arm.ClientOptions
}
