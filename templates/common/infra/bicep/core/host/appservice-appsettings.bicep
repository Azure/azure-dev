param name string
param currentAppSettings object = {}
param appSettings object

resource appService 'Microsoft.Web/sites@2022-03-01' existing = {
  name: name
}

resource settings 'Microsoft.Web/sites/config@2022-03-01' = {
  name: 'appsettings'
  parent: appService
  // appSettings is set as 2nd argument to union(). This order is important,
  // and ensures new app settings are applied over existing ones.
  properties: union(
    currentAppSettings,
    appSettings
  )
}
