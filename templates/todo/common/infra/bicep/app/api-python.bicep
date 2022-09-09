param environmentName string
param location string = resourceGroup().location
param serviceName string = 'api'
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string

module api '../../../../../common/infra/bicep/core/host/appservice-node.bicep' = {
  name: 'application-appservice-python-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
  }
}

output NAME string = api.outputs.NAME
output URI string = api.outputs.URI
output IDENTITY_PRINCIPAL_ID string = api.outputs.IDENTITY_PRINCIPAL_ID
