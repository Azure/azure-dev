param name string
param location string = resourceGroup().location
param nsgId string
param storageId string
param trafficAnalytics bool
param trafficAnalyticsInterval int = 60

@description('The resource guid of the attached workspace.')
param workspaceId string = ''

@description('Resource Id of the attached workspace.')
param workspaceResourceId string = ''
param workspaceRegion string = resourceGroup().location

resource networkWatcher 'Microsoft.Network/networkWatchers@2022-01-01' = {
  name: 'NetworkWatcher_${location}'
  location: location
  properties: {}
}

resource nsgFlowLogs 'Microsoft.Network/networkWatchers/flowLogs@2021-05-01' = {
  name: '${networkWatcher.name}/${name}'
  location: location
  properties: {
    targetResourceId: nsgId
    storageId: storageId
    enabled: true
    retentionPolicy: {
      days: 2
      enabled: true
    }
    format: {
      type: 'JSON'
      version: 2
    }
    flowAnalyticsConfiguration: {
      networkWatcherFlowAnalyticsConfiguration: {
        enabled: trafficAnalytics
        workspaceId: workspaceId
        trafficAnalyticsInterval: trafficAnalyticsInterval
        workspaceRegion: workspaceRegion
        workspaceResourceId: workspaceResourceId
      }
    }
  }
}
