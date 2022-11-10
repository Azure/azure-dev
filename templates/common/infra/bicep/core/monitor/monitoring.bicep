param logAnalyticsName string
param applicationInsightsName string
param applicationInsightsDashboardName string
param location string = resourceGroup().location
param tags object = {}
param useAPIM bool = false

module logAnalytics 'loganalytics.bicep' = {
  name: 'loganalytics'
  params: {
    name: logAnalyticsName
    location: location
    tags: tags
  }
}

module applicationInsights 'applicationinsights.bicep' = {
  name: 'applicationinsights'
  params: {
    name: applicationInsightsName
    location: location
    tags: tags
    dashboardName: applicationInsightsDashboardName
    logAnalyticsWorkspaceId: logAnalytics.outputs.id
    useAPIM: useAPIM
  }
}

output applicationInsightsConnectionString string = applicationInsights.outputs.connectionString
output applicationInsightsInstrumentationKey string = applicationInsights.outputs.instrumentationKey
output applicationInsightsName string = applicationInsights.outputs.name
output logAnalyticsWorkspaceId string = logAnalytics.outputs.id
output logAnalyticsWorkspaceName string = logAnalytics.outputs.name
