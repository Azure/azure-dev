param environmentName string
param location string = resourceGroup().location
param serviceName string
param linuxFxVersion string = 'DOTNETCORE|6.0'
param managedIdentity bool = useKeyVault
param scmDoBuildDuringDeployment bool = false
param appSettings object = {}
param useKeyVault bool = false

module web 'website.bicep' = {
  name: 'website-dotnet-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    linuxFxVersion: linuxFxVersion
    serviceName: serviceName
    managedIdentity: managedIdentity
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    appSettings: appSettings
    useKeyVault: useKeyVault
  }
}

output NAME string = web.outputs.NAME
output URI string = web.outputs.URI
output IDENTITY_PRINCIPAL_ID string = web.outputs.IDENTITY_PRINCIPAL_ID
