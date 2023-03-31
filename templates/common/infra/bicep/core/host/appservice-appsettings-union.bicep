@description('The name of the app service resource within the current resource group scope')
param name string

@description('The current app settings of the app service resource')
param currentSettings object

@description('The new/updated app settings to be applied to the app service resource')
param newSettings object

resource appService 'Microsoft.Web/sites@2022-03-01' existing = {
  name: name
}

resource settings 'Microsoft.Web/sites/config@2022-03-01' = {
  name: 'appsettings'
  parent: appService
  // appSettings is set as 2nd argument to union(). This order is important,
  // and ensures new app settings are applied over existing ones.
  properties: union(
    currentSettings,
    newSettings
  )
}
