// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
)

var (
	// hostname of the AzDo PaaS service.
	AzDoHostName = "dev.azure.com"
	// environment variable that holds the Azure DevOps PAT
	AzDoPatName = "AZURE_DEVOPS_EXT_PAT"
	// environment variable that holds the Azure DevOps Organization Name
	AzDoEnvironmentOrgName = "AZURE_DEVOPS_ORG_NAME"
	// Environment Configuration name used to store the project Id
	AzDoEnvironmentProjectIdName = "AZURE_DEVOPS_PROJECT_ID"
	// Environment Configuration name used to store the project name
	AzDoEnvironmentProjectName = "AZURE_DEVOPS_PROJECT_NAME"
	// Environment Configuration name used to store repo ID
	AzDoEnvironmentRepoIdName = "AZURE_DEVOPS_REPOSITORY_ID"
	// Environment Configuration name used to store the Repo Name
	AzDoEnvironmentRepoName = "AZURE_DEVOPS_REPOSITORY_NAME"
	// web url for the configured repo. This is displayed on a the command line after a successful
	// invocation of azd pipeline config
	AzDoEnvironmentRepoWebUrl = "AZURE_DEVOPS_REPOSITORY_WEB_URL"
	// success message after azd pipeline config is successful
	AzdoConfigSuccessMessage = "\nSuccessfully configured Azure DevOps Repository %s\n"
	// name of the azure pipeline that will be created
	AzurePipelineName = "Azure Dev Deploy"
	// path to the azure pipeline yaml
	AzurePipelineYamlPath = ".azdo/pipelines/azure-dev.yml"
	// target Azure Cloud
	CloudEnvironment = "AzureCloud"
	// default branch for pipeline and branch policy
	DefaultBranch = "main"
	// azure devops project description
	AzDoProjectDescription = "Azure Developer CLI Project"
	// name of the service connection that will be used in the AzDo project. This will store the Azure service principal
	ServiceConnectionName = "azconnection"
)

// helper method to return an Azure DevOps connection used the AzDo go sdk
func GetConnection(
	ctx context.Context, organization string, personalAccessToken string) (*azuredevops.Connection, error) {
	if organization == "" {
		return nil, fmt.Errorf("organization name is required")
	}

	if personalAccessToken == "" {
		return nil, fmt.Errorf("personal access token is required")
	}

	organizationUrl := fmt.Sprintf("https://%s/%s", AzDoHostName, organization)
	return azuredevops.NewPatConnection(organizationUrl, personalAccessToken), nil
}
