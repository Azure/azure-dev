param environmentName string
param location string = resourceGroup().location
param principalId string = ''

module appServicePlanResources '../../../../../../common/infra/bicep/modules/appserviceplan-site.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module webResources '../../../../../common/infra/appservice/bicep/modules/web.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    applicationInsightsResources
    appServicePlanResources
  ]
}

module apiResources '../../../../../common/infra/appservice/bicep/modules/api-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosEndpoint: cosmosResources.outputs.AZURE_COSMOS_ENDPOINT
  }
  dependsOn: [
    applicationInsightsResources
    keyVaultResources
    appServicePlanResources
  ]
}

module keyVaultResources '../../../../../../common/infra/bicep/modules/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

module cosmosResources '../../../../../common/infra/modules/cosmos-sql.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
  dependsOn: [
    keyVaultResources
  ]
}

module apiCosmosSqlRoleResources '../../../../../common/infra/modules/cosmos-sql-role-assign.bicep' = {
  name: 'api-cosmos-sql-role-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosRoleDefinitionId: cosmosResources.outputs.AZURE_COSMOS_SQL_ROLE_DEFINITION_ID
    principalId: apiResources.outputs.API_PRINCIPAL_ID
  }
}

module logAnalyticsWorkspaceResources '../../../../../../common/infra/bicep/modules/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module applicationInsightsResources '../../../../../../common/infra/bicep/modules/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    environmentName: environmentName
    location: location
    workspaceId: logAnalyticsWorkspaceResources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

output AZURE_COSMOS_ENDPOINT string = cosmosResources.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmosResources.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmosResources.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVaultResources.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = webResources.outputs.WEB_URI
output API_URI string = apiResources.outputs.API_URI
