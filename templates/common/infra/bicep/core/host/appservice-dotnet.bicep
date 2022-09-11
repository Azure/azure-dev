param environmentName string
param location string = resourceGroup().location
param serviceName string
param linuxFxVersion string = 'DOTNETCORE|6.0'
param scmDoBuildDuringDeployment bool = false
param appSettings object = {}
param keyVaultName string = ''
param useKeyVault bool = !(empty(keyVaultName))
param managedIdentity bool = useKeyVault
param applicationInsightsName string
param appServicePlanId string
param allowedOrigins array = []

module web 'appservice.bicep' = {
  name: 'appservice-dotnet-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    linuxFxVersion: linuxFxVersion
    serviceName: serviceName
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    appSettings: appSettings
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
    useKeyVault: useKeyVault
    managedIdentity: managedIdentity
    allowedOrigins: allowedOrigins
  }
}

output NAME string = web.outputs.NAME
output URI string = web.outputs.URI
output IDENTITY_PRINCIPAL_ID string = web.outputs.IDENTITY_PRINCIPAL_ID
