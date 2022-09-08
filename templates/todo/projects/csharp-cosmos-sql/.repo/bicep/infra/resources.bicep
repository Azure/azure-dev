param environmentName string
param location string = resourceGroup().location
param principalId string = ''

module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-sites.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module web '../../../../../common/infra/core/application/web-node.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    applicationInsights
    appServicePlan
  ]
}

module api '../../../../../common/infra/core/application/api-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    applicationInsights
    keyVault
    appServicePlan
  ]
}

module apiCosmosConfig '../../../../../../common/infra/bicep/core/host/appservice-config-cosmos.bicep' = {
  name: 'api-cosmos-config-resources'
  params: {
    appServiceName: api.outputs.NAME
    cosmosDatabaseName: cosmos.outputs.AZURE_COSMOS_DATABASE_NAME
    cosmosConnectionStringKey: cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
    cosmosEndpoint: cosmos.outputs.AZURE_COSMOS_ENDPOINT
  }
}

module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

module cosmos '../../../../../../common/infra/bicep/core/database/cosmos-sql-db.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    cosmosDatabaseName: 'Todo'
    principalIds: [ principalId, api.outputs.IDENTITY_PRINCIPAL_ID ]
    containers: [
      {
        name: 'TodoList'
        id: 'TodoList'
        partitionKey: '/id'
      }
      {
        name: 'TodoItem'
        id: 'TodoItem'
        partitionKey: '/id'
      }
    ]
  }
  dependsOn: [
    keyVault
  ]
}

module logAnalytics '../../../../../../common/infra/bicep/core/monitor/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module applicationInsights '../../../../../../common/infra/bicep/core/monitor/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    environmentName: environmentName
    location: location
    workspaceId: logAnalytics.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = web.outputs.URI
output API_URI string = api.outputs.URI
