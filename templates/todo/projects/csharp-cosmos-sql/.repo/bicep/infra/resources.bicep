param environmentName string
param location string = resourceGroup().location
param principalId string = ''

// The application frontend
module web '../../../../../common/infra/bicep/app/web-appservice.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.APPLICATIONINSIGHTS_NAME
    appServicePlanId: appServicePlan.outputs.AZURE_APP_SERVICE_PLAN_ID
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-appservice-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.APPLICATIONINSIGHTS_NAME
    appServicePlanId: appServicePlan.outputs.AZURE_APP_SERVICE_PLAN_ID
    keyVaultName: keyVault.outputs.AZURE_KEY_VAULT_NAME
    allowedOrigins: [web.outputs.URI]
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-sql.bicep' = {
  name: 'cosmos-resources'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.AZURE_KEY_VAULT_NAME
    principalIds: [ principalId, api.outputs.IDENTITY_PRINCIPAL_ID ]
  }
}

// Configure api to use cosmos
module apiCosmosConfig '../../../../../../common/infra/bicep/core/host/appservice-config-cosmos.bicep' = {
  name: 'api-cosmos-config-resources'
  params: {
    appServiceName: api.outputs.NAME
    cosmosDatabaseName: cosmos.outputs.AZURE_COSMOS_DATABASE_NAME
    cosmosConnectionStringKey: cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
    cosmosEndpoint: cosmos.outputs.AZURE_COSMOS_ENDPOINT
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-sites.bicep' = {
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

output AZURE_COSMOS_ENDPOINT string = cosmos.outputs.AZURE_COSMOS_ENDPOINT
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.AZURE_COSMOS_CONNECTION_STRING_KEY
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.AZURE_COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = web.outputs.URI
output API_URI string = api.outputs.URI
