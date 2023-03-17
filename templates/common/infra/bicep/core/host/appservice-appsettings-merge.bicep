param name string
param appSettings object

resource appService 'Microsoft.Web/sites@2022-03-01' existing = {
  name: name
}

module apply 'appservice-appsettings.bicep' = {
  name: 'appservice-appsettings'
  params: {
    name: appService.name
    currentAppSettings: list('${appService.id}/config/appSettings', '2022-03-01').properties
    appSettings: appSettings
  }
}
