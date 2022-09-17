param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string
param serviceName string = 'api'

module api '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: '${serviceName}-appservice-node-module'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
    scmDoBuildDuringDeployment: true
    serviceName: serviceName
  }
}

output API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output API_NAME string = api.outputs.name
output API_URI string = api.outputs.uri
