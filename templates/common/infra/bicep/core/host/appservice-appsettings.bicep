@description('The name of the app service resource within the current resource group scope')
param name string

@description('The new/updated app settings to be merged into existing app service settings')
param appSettings object

resource appService 'Microsoft.Web/sites@2022-03-01' existing = {
  name: name
}

resource appServiceConfig 'Microsoft.Web/sites/config@2022-03-01' existing = {
  name: 'appsettings'
  parent: appService
}

// The retrieval of the current app settings needs to be completed in seperate module
// from the applying of the app settings otherwise ARM will throw circular dependency errors.
module appSettingsUnion 'appservice-appsettings-union.bicep' = {
  name: 'appSettingsUnion'
  params: {
    name: name
    currentSettings: appServiceConfig.list().properties
    newSettings: appSettings
  }
}
