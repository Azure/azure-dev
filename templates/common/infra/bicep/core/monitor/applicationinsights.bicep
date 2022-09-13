param environmentName string
param location string = resourceGroup().location
param logAnalyticsWorkspaceId string

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' = {
  name: '${abbrs.insightsComponents}${resourceToken}'
  location: location
  tags: tags
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: logAnalyticsWorkspaceId
  }
}

module applicationInsightsDashboard 'applicationinsights-dashboard.bicep' = {
  name: 'application-insights-dashboard'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: applicationInsights.name
  }
}

output APPLICATIONINSIGHTS_NAME string = applicationInsights.name
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.properties.ConnectionString
