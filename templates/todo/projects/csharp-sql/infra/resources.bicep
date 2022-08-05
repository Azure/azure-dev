param location string
param resourceToken string
param tags object

var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

resource web 'Microsoft.Web/sites@2021-03-01' = {
  name: '${abbrs.webSitesAppService}web-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'web' })
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      alwaysOn: true
      ftpsState: 'FtpsOnly'
    }
    httpsOnly: true
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'false'
      APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
    }
  }

  resource logs 'config' = {
    name: 'logs'
    properties: {
      applicationLogs: {
        fileSystem: {
          level: 'Verbose'
        }
      }
      detailedErrorMessages: {
        enabled: true
      }
      failedRequestsTracing: {
        enabled: true
      }
      httpLogs: {
        fileSystem: {
          enabled: true
          retentionInDays: 1
          retentionInMb: 35
        }
      }
    }
  }
}

resource api 'Microsoft.Web/sites@2021-03-01' = {
  name: '${abbrs.webSitesAppService}api-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'api' })
  kind: 'app,linux'
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      alwaysOn: true
      ftpsState: 'FtpsOnly'
    }
    httpsOnly: true
  }

  identity: {
    type: 'SystemAssigned'
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      AZURE_SQL_CONNECTION_STRING: AZURE_SQL_CONNECTION_STRING
      APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
    }
  }

  resource logs 'config' = {
    name: 'logs'
    properties: {
      applicationLogs: {
        fileSystem: {
          level: 'Verbose'
        }
      }
      detailedErrorMessages: {
        enabled: true
      }
      failedRequestsTracing: {
        enabled: true
      }
      httpLogs: {
        fileSystem: {
          enabled: true
          retentionInDays: 1
          retentionInMb: 35
        }
      }
    }
  }
}

resource appServicePlan 'Microsoft.Web/serverfarms@2021-03-01' = {
  name: '${abbrs.webServerFarms}${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'B1'
  }
}

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2020-03-01-preview' = {
  name: '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
  location: location
  tags: tags
  properties: any({
    retentionInDays: 30
    features: {
      searchVersion: 1
    }
    sku: {
      name: 'PerGB2018'
    }
  })
}

module applicationInsightsResources '../../../../common/infra/bicep/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    resourceToken: resourceToken
    location: location
    tags: tags
    workspaceId: logAnalyticsWorkspace.id
  }
}

// 2021-11-01-preview because that is latest valid version
resource sqlServer 'Microsoft.Sql/servers@2021-11-01-preview' = {
  name: '${abbrs.sqlServers}${resourceToken}'
  location: location
  tags: tags
  properties: {
    version: '12.0'
    minimalTlsVersion: '1.2'
    publicNetworkAccess: 'Enabled'
    administrators: {
      administratorType: 'ActiveDirectory'
      principalType: 'User'
      sid: api.identity.principalId
      login: 'activedirectoryadmin'
      tenantId: api.identity.tenantId
      azureADOnlyAuthentication: true
    }
  }

  resource database 'databases' = {
    name: 'ToDo'
    location: location
  }

  resource firewall 'firewallRules' = {
    name: 'Azure Services'
    properties: {
      startIpAddress: '0.0.0.0'
      endIpAddress: '0.0.0.0'
    }
  }
}

// Defined as a var here because it is used above

var AZURE_SQL_CONNECTION_STRING = 'Server=${sqlServer.properties.fullyQualifiedDomainName}; Authentication=Active Directory Default; Database=${sqlServer::database.name};'

output AZURE_SQL_CONNECTION_STRING string = AZURE_SQL_CONNECTION_STRING
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = 'https://${web.properties.defaultHostName}'
output API_URI string = 'https://${api.properties.defaultHostName}'
