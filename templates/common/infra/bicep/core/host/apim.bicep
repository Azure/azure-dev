param name string
param location string = resourceGroup().location
param tags object = {}

@description('The email address of the owner of the service')
@minLength(1)
param publisherEmail string = 'noreply@microsoft.com'

@description('The name of the owner of the service')
@minLength(1)
param publisherName string = 'n/a'

@description('The pricing tier of this API Management service')
@allowed([
  'Consumption'
  'Developer'
  'Standard'
  'Premium'
])
param sku string

@description('The instance size of this API Management service.')
@allowed([ 0, 1, 2 ])
param skuCount int

@description('Azure Application Insights Resource Id')
param appInsightsResourceId string

@description('Azure Application Insights Instrumentation Key')
param appInsightsInstrumentationKey string

resource apimService 'Microsoft.ApiManagement/service@2021-08-01' = {
  name: name
  location: location
  tags: union(tags, { 'azd-service-name': name })
  sku: {
    name: sku
    capacity: skuCount
  }
  properties: {
    publisherEmail: publisherEmail
    publisherName: publisherName
  }
}

resource apimLogger 'Microsoft.ApiManagement/service/loggers@2021-12-01-preview' = {
  name: 'app-insights-logger'
  parent: apimService
  properties: {
    credentials: {
      instrumentationKey: appInsightsInstrumentationKey
    }
    description: 'Logger to Azure Application Insights'
    isBuffered: false
    loggerType: 'applicationInsights'
    resourceId: appInsightsResourceId
  }
}


