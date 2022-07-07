param location string
param principalId string = ''
param resourceToken string
param tags object

resource web 'Microsoft.Web/sites@2021-03-01' = {
  name: 'app-web-${resourceToken}'
  location: location
  tags: union(tags, {
      'azd-service-name': 'web'
    })
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
      'SCM_DO_BUILD_DURING_DEPLOYMENT': 'false'
      'APPLICATIONINSIGHTS_CONNECTION_STRING': applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
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

resource api 'Microsoft.Web/sites@2021-01-15' = {
  name: 'app-api-${resourceToken}'
  location: location
  tags: union(tags, {
      'azd-service-name': 'api'
    })
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
      'AZURE_COSMOS_ENDPOINT': cosmos.properties.documentEndpoint
      'AZURE_COSMOS_DATABASE_NAME': cosmos::database.name
      'APPLICATIONINSIGHTS_CONNECTION_STRING': applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
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
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'B1'
  }
}

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2020-03-01-preview' = {
  name: 'log-${resourceToken}'
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

module applicationInsightsResources './applicationinsights.bicep' = {
  name: 'applicationinsights-${resourceToken}'
  params: {
    resourceToken: resourceToken
    location: location
    tags: tags
    workspaceId: logAnalyticsWorkspace.id
  }
}

resource cosmos 'Microsoft.DocumentDB/databaseAccounts@2021-10-15' = {
  name: 'cosmos-${resourceToken}'
  location: location
  tags: tags
  properties: {
    locations: [
      {
        locationName: location
        failoverPriority: 0
        isZoneRedundant: false
      }
    ]
    databaseAccountOfferType: 'Standard'
  }

  resource database 'sqlDatabases' = {
    name: 'Todo'
    properties: {
      resource: {
        id: 'Todo'
      }
    }

    resource list 'containers' = {
      name: 'TodoList'
      properties: {
        resource: {
          id: 'TodoList'
          partitionKey: {
            paths: [
              '/id'
            ]
          }
        }
        options: {}
      }
    }

    resource item 'containers' = {
      name: 'TodoItem'
      properties: {
        resource: {
          id: 'TodoItem'
          partitionKey: {
            paths: [
              '/id'
            ]
          }
        }
        options: {}
      }
    }
  }

  resource roleDefinition 'sqlroleDefinitions' = {
    name: guid(cosmos.id, resourceToken, 'sql-role')
    properties: {
      assignableScopes: [
        cosmos.id
      ]
      permissions: [
        {
          dataActions: [
            'Microsoft.DocumentDB/databaseAccounts/readMetadata'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/*'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/*'
          ]
          notDataActions: []
        }
      ]
      roleName: 'Reader Writer'
      type: 'CustomRole'
    }
  }

  resource userRole 'sqlRoleAssignments' = if (!empty(principalId)) {
    name: guid(roleDefinition.id, principalId, cosmos.id)
    properties: {
      principalId: principalId
      roleDefinitionId: roleDefinition.id
      scope: cosmos.id
    }
  }

  resource appRole 'sqlRoleAssignments' = {
    name: guid(roleDefinition.id, api.id, cosmos.id)
    properties: {
      principalId: api.identity.principalId
      roleDefinitionId: roleDefinition.id
      scope: cosmos.id
    }

    dependsOn: [
      userRole
    ]
  }
}

output AZURE_COSMOS_ENDPOINT string = cosmos.properties.documentEndpoint
output AZURE_COSMOS_DATABASE_NAME string = cosmos::database.name
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsightsResources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = 'https://${web.properties.defaultHostName}'
output API_URI string = 'https://${api.properties.defaultHostName}'
