param currentConfigProperties object
param additionalConfigProperties object
param resourceName string
param configName string

resource siteConfigUnion 'Microsoft.Web/sites/config@2022-03-01' = {
  name: '${resourceName}/${configName}'
  properties: union(currentConfigProperties, additionalConfigProperties)
}
