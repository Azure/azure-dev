param name string
param appSettings object

resource staticSite 'Microsoft.Web/staticSites@2022-03-01' existing = {
  name: name
}

resource settings 'Microsoft.Web/staticSites/config@2022-03-01' = {
  name: 'appsettings'
  parent: staticSite
  // appSettings is set as 2nd argument to union(). This order is important,
  // and ensures new app settings are applied over existing ones.
  properties: union(
    list('${staticSite.id}/config/appSettings', '2022-03-01').properties,
    appSettings
  )
}
