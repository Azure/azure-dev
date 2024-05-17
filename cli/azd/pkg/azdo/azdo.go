// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

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

type Connection struct {
	*azuredevops.Connection
	OrganizationName string
	OrganizationId   string
}

func (con *Connection) getOrganizationId(ctx context.Context) (string, error) {
	rawClient := con.GetClientByUrl(con.BaseUrl)

	query := make(url.Values)
	query.Add("accountName", con.OrganizationName)
	// resourceAreas id is the same for all accounts:
	// nolint:lll
	//https://learn.microsoft.com/en-us/azure/devops/extend/develop/work-with-urls?view=azure-devops&tabs=http#with-the-organizations-name
	url := fmt.Sprintf(
		"https://dev.azure.com/_apis/resourceAreas/79134C72-4A58-4B42-976C-04E7115F32BF?accountName=%s",
		con.OrganizationName)

	orgRequest, error := rawClient.CreateRequestMessage(ctx, http.MethodGet, url, "7.2-preview.1", nil, "", "", nil)
	if error != nil {
		return "", error
	}

	httpResponse, error := rawClient.SendRequest(orgRequest)
	if error != nil {
		return "", error
	}

	bodyStringResponse, error := io.ReadAll(httpResponse.Body)
	if error != nil {
		return "", error
	}
	return string(bodyStringResponse), nil
}

// helper method to return an Azure DevOps connection used the AzDo go sdk
func GetConnection(
	ctx context.Context, organization string, personalAccessToken string) (Connection, error) {
	if organization == "" {
		return Connection{}, fmt.Errorf("organization name is required")
	}

	if personalAccessToken == "" {
		return Connection{}, fmt.Errorf("personal access token is required")
	}

	organizationUrl := fmt.Sprintf("https://%s/%s", AzDoHostName, organization)
	connection := azuredevops.NewPatConnection(organizationUrl, personalAccessToken)

	adoConnection := Connection{
		Connection:       connection,
		OrganizationName: organization,
	}

	orgId, err := adoConnection.getOrganizationId(ctx)
	if err != nil {
		return Connection{}, fmt.Errorf("getting organization id: %w", err)
	}
	adoConnection.OrganizationId = orgId

	return adoConnection, nil
}
