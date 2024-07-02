@description('Resource name for backend Web App or Function App')
param apiAppName string = ''

@description('Resource name to uniquely identify this API within the API Management service instance')
@minLength(1)
param apiName string 

@description('Resource ID for the existing apim service')
param apimServiceId string

var appNameForBicep = !empty(apiAppName) ? apiAppName : 'placeholderName'

resource apiAppProperties 'Microsoft.Web/sites/config@2022-03-01' = if (!empty(apiAppName)) {
  name: '${appNameForBicep}/web'
  kind: 'string'
  properties: {
      apiManagementConfig: {
        id: '${apimServiceId}/apis/${apiName}'
      }
  }
}
