param environmentName string
param location string = resourceGroup().location
param serviceName string = 'api'
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string
param allowedOrigins array = []

module api '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: 'api-appservice-node-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
    scmDoBuildDuringDeployment: true
    allowedOrigins: allowedOrigins
  }
}

output NAME string = api.outputs.NAME
output URI string = api.outputs.URI
output IDENTITY_PRINCIPAL_ID string = api.outputs.IDENTITY_PRINCIPAL_ID
