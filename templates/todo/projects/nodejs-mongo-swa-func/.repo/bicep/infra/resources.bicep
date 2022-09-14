param environmentName string
param location string = resourceGroup().location
param principalId string = ''

// The application frontend
module web '../../../../../common/infra/bicep/app/web-staticwebapp.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-function-node.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
    keyVaultName: keyVault.outputs.keyVaultName
    storageAccountName: storage.outputs.name
    allowedOrigins: [ web.outputs.webUri ]
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Configure api to use cosmos
module apiCosmosConfig '../../../../../../common/infra/bicep/core/host/appservice-config-cosmos.bicep' = {
  name: 'api-cosmos-config-resources'
  params: {
    appServiceName: api.outputs.name
    cosmosDatabaseName: cosmos.outputs.cosmosDatabaseName
    cosmosConnectionStringKey: cosmos.outputs.cosmosConnectionStringKey
    cosmosEndpoint: cosmos.outputs.cosmosEndpoint
  }
}

// Backing storage for Azure functions backend API
module storage '../../../../../../common/infra/bicep/core/storage/storage-account.bicep' = {
  name: 'storage-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-functions.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

// Store secrets in a keyvault
module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

output apiUri string = api.outputs.apiUri
output applicationInsightsConnectionString string = monitoring.outputs.applicationInsightsConnectionString
output cosmosConnectionStringKey string = cosmos.outputs.cosmosConnectionStringKey
output cosmosDatabaseName string = cosmos.outputs.cosmosDatabaseName
output cosmosEndpoint string = cosmos.outputs.cosmosEndpoint
output keyVaultEndpoint string = keyVault.outputs.keyVaultEndpoint
output webUri string = web.outputs.webUri
