package azdo

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops"
)

var (
	AzDoHostName                 = "dev.azure.com"                                          // hostname of the AzDo PaaS service.
	AzDoPatName                  = "AZURE_DEVOPS_EXT_PAT"                                   // environment variable that holds the Azure DevOps PAT
	AzDoEnvironmentOrgName       = "AZURE_DEVOPS_ORG_NAME"                                  // environment variable that holds the Azure DevOps Organization Name
	AzDoEnvironmentProjectIdName = "AZURE_DEVOPS_PROJECT_ID"                                // Environment Configuration name used to store the project Id
	AzDoEnvironmentProjectName   = "AZURE_DEVOPS_PROJECT_NAME"                              // Environment Configuration name used to store the project name
	AzDoEnvironmentRepoIdName    = "AZURE_DEVOPS_REPOSITORY_ID"                             // Environment Configuration name used to store repo ID
	AzDoEnvironmentRepoName      = "AZURE_DEVOPS_REPOSITORY_NAME"                           // Environment Configuration name used to store the Repo Name
	AzDoEnvironmentRepoWebUrl    = "AZURE_DEVOPS_REPOSITORY_WEB_URL"                        // web url for the configured repo. This is displayed on a the command line after a successful invocation of azd pipeline config
	AzdoConfigSuccessMessage     = "\nSuccessfully configured Azure DevOps Repository %s\n" // success message after azd pipeline config is successful
	AzurePipelineName            = "Azure Dev Deploy"                                       // name of the azure pipeline that will be created
	AzurePipelineYamlPath        = ".azdo/pipelines/azure-dev.yml"                          // path to the azure pipeline yaml
	CloudEnvironment             = "AzureCloud"                                             // target Azure Cloud
	DefaultBranch                = "master"                                                 // default branch for pipeline and branch policy
	AzDoProjectDescription       = "Azure Dev CLI Project"                                  // azure devops project description
	ServiceConnectionName        = "azconnection"                                           // name of the service connection that will be used in the AzDo project. This will store the Azure service principal
)

type AzureServicePrincipalCredentials struct {
	TenantId       string `json:"tenantId"`
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	SubscriptionId string `json:"subscriptionId"`
}

// helper method to return an Azure DevOps connection used the AzDo go sdk
func GetAzdoConnection(ctx context.Context, organization string, personalAccessToken string) (*azuredevops.Connection, error) {
	if organization == "" {
		return nil, fmt.Errorf("organization name is required")
	}

	if personalAccessToken == "" {
		return nil, fmt.Errorf("personal access token is required")
	}

	organizationUrl := fmt.Sprintf("https://%s/%s", AzDoHostName, organization)
	connection := azuredevops.NewPatConnection(organizationUrl, personalAccessToken)

	return connection, nil
}
