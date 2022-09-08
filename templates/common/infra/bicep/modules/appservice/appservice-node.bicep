param environmentName string
param location string = resourceGroup().location
param serviceName string
param linuxFxVersion string = 'NODE|16-lts'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param managedIdentity bool = useKeyVault
param scmDoBuildDuringDeployment bool = false
param appSettings object = {}
param useKeyVault bool = false

module web 'appservice.bicep' = {
  name: 'appservice-node-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    linuxFxVersion: linuxFxVersion
    serviceName: serviceName
    appCommandLine: appCommandLine
    managedIdentity: managedIdentity
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    appSettings: appSettings
    useKeyVault: useKeyVault
  }
}

output NAME string = web.outputs.NAME
output URI string = web.outputs.URI
output IDENTITY_PRINCIPAL_ID string = web.outputs.IDENTITY_PRINCIPAL_ID
