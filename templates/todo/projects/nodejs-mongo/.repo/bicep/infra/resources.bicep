param environmentName string
param location string = resourceGroup().location
param principalId string = ''

// The application frontend
module web '../../../../../common/infra/bicep/app/web-appservice.bicep' = {
  name: 'web'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-appservice-node.bicep' = {
  name: 'api'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
    keyVaultName: keyVault.outputs.keyVaultName
    allowedOrigins: [ web.outputs.WEB_URI ]
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo-db.bicep' = {
  name: 'cosmos'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Configure api to use cosmos
module apiCosmosConfig '../../../../../../common/infra/bicep/core/host/appservice-config-cosmos.bicep' = {
  name: 'api-cosmos-config'
  params: {
    appServiceName: api.outputs.API_NAME
    cosmosDatabaseName: cosmos.outputs.cosmosDatabaseName
    cosmosConnectionStringKey: cosmos.outputs.cosmosConnectionStringKey
    cosmosEndpoint: cosmos.outputs.cosmosEndpoint
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-sites.bicep' = {
  name: 'appserviceplan'
  params: {
    environmentName: environmentName
    location: location
  }
}

// Store secrets in a keyvault
module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring'
  params: {
    environmentName: environmentName
    location: location
  }
}

output API_URI string = api.outputs.API_URI
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.cosmosConnectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.cosmosDatabaseName
output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.cosmosEndpoint
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.keyVaultEndpoint
output WEB_URI string = web.outputs.WEB_URI
