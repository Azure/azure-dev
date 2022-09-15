param environmentName string
param location string = resourceGroup().location

param serviceName string = 'web'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param applicationInsightsName string
param appServicePlanId string

module web '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: 'web-appservice-node-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    appCommandLine: appCommandLine
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
  }
}

output webIdentityPrincipalId string = web.outputs.identityPrincipalId
output webName string = web.outputs.name
output webUri string = web.outputs.uri
