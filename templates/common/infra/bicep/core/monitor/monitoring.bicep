param environmentName string
param location string = resourceGroup().location

module logAnalytics 'loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module applicationInsights 'applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    environmentName: environmentName
    location: location
    logAnalyticsWorkspaceId: logAnalytics.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}

output APPLICATIONINSIGHTS_NAME string = applicationInsights.outputs.APPLICATIONINSIGHTS_NAME
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output AZURE_LOG_ANALYTICS_WORKSPACE_NAME string = logAnalytics.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_NAME
output AZURE_LOG_ANALYTICS_WORKSPACE_ID string = logAnalytics.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
