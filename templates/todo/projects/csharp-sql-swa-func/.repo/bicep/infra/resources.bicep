param environmentName string
param location string = resourceGroup().location
param principalId string = ''

@secure()
param sqlAdminPassword string

@secure()
param appUserPassword string

// The application frontend
module web '../../../../../common/infra/bicep/app/web-staticwebapp.bicep' = {
  name: 'web'
  params: {
    environmentName: environmentName
    location: location
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-functions-csharp.bicep' = {
  name: 'api'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
    keyVaultName: keyVault.outputs.keyVaultName
    storageAccountName: storage.outputs.name
    allowedOrigins: [ web.outputs.WEB_URI ]
  }
}

// The application database
module sqlServer '../../../../../common/infra/bicep/app/sqlserver.bicep' = {
  name: 'sql'
  params: {
    environmentName: environmentName
    location: location
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Configure api to use sql
module apiSqlServerConfig '../../../../../../common/infra/bicep/core/host/appservice-config-sqlserver.bicep' = {
  name: 'api-sqlserver-config'
  params: {
    appServiceName: api.outputs.API_NAME
    sqlConnectionStringKey: sqlServer.outputs.sqlConnectionStringKey
  }
}

// Backing storage for Azure functions backend API
module storage '../../../../../../common/infra/bicep/core/storage/storage-account.bicep' = {
  name: 'storage'
  params: {
    environmentName: environmentName
    location: location
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-functions.bicep' = {
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
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.keyVaultEndpoint
output AZURE_KEY_VAULT_NAME string = keyVault.name
output AZURE_SQL_CONNECTION_STRING_KEY string = sqlServer.outputs.sqlConnectionStringKey
output WEB_URI string = web.outputs.WEB_URI
