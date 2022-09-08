param currentConfigProperties object
param additionalConfigProperties object
param appServiceName string
param configName string

resource siteConfigUnion 'Microsoft.Web/sites/config@2022-03-01' = {
  name: '${appServiceName}/${configName}'
  properties: union(currentConfigProperties, additionalConfigProperties)
}
