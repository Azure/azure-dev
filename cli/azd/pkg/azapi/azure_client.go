// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

var (
	ErrAzCliNotLoggedIn         = errors.New("cli is not logged in. Try running \"az login\" to fix")
	ErrAzCliRefreshTokenExpired = errors.New("refresh token has expired. Try running \"az login\" to fix")
)

func NewAzureClient(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *AzureClient {
	return &AzureClient{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

type AzureClient struct {
	credentialProvider          account.SubscriptionCredentialProvider
	armClientOptions            *arm.ClientOptions
	cognitiveAccountsCache      clientCache[*armcognitiveservices.AccountsClient]
	deletedCognitiveCache       clientCache[*armcognitiveservices.DeletedAccountsClient]
	cognitiveModelsCache        clientCache[*armcognitiveservices.ModelsClient]
	cognitiveUsagesCache        clientCache[*armcognitiveservices.UsagesClient]
	cognitiveResourceSkusCache  clientCache[*armcognitiveservices.ResourceSKUsClient]
	webAppsCache                clientCache[*armappservice.WebAppsClient]
	zipDeployCache              clientCache[*azsdk.ZipDeployClient]
	apimCache                   clientCache[*armapimanagement.ServiceClient]
	apimDeletedCache            clientCache[*armapimanagement.DeletedServicesClient]
	staticSitesCache            clientCache[*armappservice.StaticSitesClient]
	logAnalyticsWorkspacesCache clientCache[*armoperationalinsights.WorkspacesClient]
	managedHsmCache             clientCache[*armkeyvault.ManagedHsmsClient]
	appConfigCache              clientCache[*armappconfiguration.ConfigurationStoresClient]
}
