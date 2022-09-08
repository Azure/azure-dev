param environmentName string
param location string = resourceGroup().location
param serviceName string = 'web'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string = ''

module web '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: 'application-appservice-node-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    appCommandLine: appCommandLine
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
  }
}

output NAME string = web.outputs.NAME
output URI string = web.outputs.URI
output IDENTITY_PRINCIPAL_ID string = web.outputs.IDENTITY_PRINCIPAL_ID
