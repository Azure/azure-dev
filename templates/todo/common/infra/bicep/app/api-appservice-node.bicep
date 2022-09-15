param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string
param serviceName string = 'api'

module api '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: 'api-appservice-node-${serviceName}'
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

output apiIdentityPrincipalId string = api.outputs.identityPrincipalId
output apiName string = api.outputs.name
output apiUri string = api.outputs.uri
