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
module api '../../../../../common/infra/bicep/app/api-appservice-dotnet.bicep' = {
  name: 'api'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
    keyVaultName: keyVault.outputs.name
    allowedOrigins: [ web.outputs.WEB_URI ]
    appSettings: {
      AZURE_COSMOS_CONNECTION_STRING_KEY: cosmos.outputs.cosmosConnectionStringKey
      AZURE_COSMOS_DATABASE_NAME: cosmos.outputs.cosmosDatabaseName
      AZURE_COSMOS_ENDPOINT: cosmos.outputs.cosmosEndpoint
    }
  }
}

// Give the API access to KeyVault
module apiKeyVaultAccess '../../../../../../common/infra/bicep/core/security/keyvault-access.bicep' = {
  name: 'api-keyvault-access'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.name
    principalId: api.outputs.API_IDENTITY_PRINCIPAL_ID
  }
}

// Give the API the role to access Cosmos
module apiCosmosSqlRoleAssign '../../../../../../common/infra/bicep/core/database/cosmos/sql/cosmos-sql-role-assign.bicep' = {
  name: 'api-cosmos-access'
  params: {
    environmentName: environmentName
    location: location
    cosmosRoleDefinitionId: cosmos.outputs.cosmosSqlRoleDefinitionId
    principalId: api.outputs.API_IDENTITY_PRINCIPAL_ID
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-sql-db.bicep' = {
  name: 'cosmos'
  params: {
    environmentName: environmentName
    location: location
    keyVaultName: keyVault.outputs.name
    principalIds: [ principalId ]
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan.bicep' = {
  name: 'appserviceplan'
  params: {
    environmentName: environmentName
    location: location
    sku: {
      name: 'B1'
    }
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
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.endpoint
output WEB_URI string = web.outputs.WEB_URI
