param name string
param dashboardName string
param location string = resourceGroup().location
param tags object = {}

param logAnalyticsWorkspaceId string
param useAPIM bool

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' = {
  name: name
  location: location
  tags: tags
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: logAnalyticsWorkspaceId
  }
}

module applicationInsightsDashboard 'applicationinsights-dashboard.bicep' = if (!useAPIM) {
  name: 'application-insights-dashboard'
  params: {
    name: dashboardName
    location: location
    applicationInsightsName: applicationInsights.name
  }
}

module applicationInsightsAPIMDashboard 'applicationinsights-dashboard.bicep' = if (useAPIM) {
  name: 'application-insights-apim-dashboard'
  params: {
    name: dashboardName
    location: location
    applicationInsightsName: applicationInsights.name
  }
}

output connectionString string = applicationInsights.properties.ConnectionString
output instrumentationKey string = applicationInsights.properties.InstrumentationKey
output name string = applicationInsights.name
