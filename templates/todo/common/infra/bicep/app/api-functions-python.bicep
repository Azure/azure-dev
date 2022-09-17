param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param applicationInsightsName string
param appServicePlanId string
param appSettings object = {}
param keyVaultName string
param serviceName string = 'api'
param storageAccountName string

module api '../../../../../common/infra/bicep/core/host/functions-python.bicep' = {
  name: '${serviceName}-functions-python-module'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    appSettings: appSettings
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
    serviceName: serviceName
    storageAccountName: storageAccountName
  }
}

output API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output API_NAME string = api.outputs.name
output API_URI string = api.outputs.uri
