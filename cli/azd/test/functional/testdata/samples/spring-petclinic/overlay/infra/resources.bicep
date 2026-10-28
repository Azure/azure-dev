param environmentName string
param location string = resourceGroup().location

@allowed([
  'h2'
  'postgresql'
])
param databaseType string

@secure()
param databasePassword string = ''

param imageName string

var isPostgres = databaseType == 'postgresql'
var placeholderImage = 'mcr.microsoft.com/k8se/quickstart:latest'
var isPlaceholder = imageName == placeholderImage
var resourceToken = toLower(uniqueString(subscription().id, resourceGroup().id, environmentName))
var tags = {
  'azd-env-name': environmentName
}
var postgresDatabase = 'petclinic'
var postgresUser = 'petclinicadmin'

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: 'cr${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Basic'
  }
  properties: {
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
  }
}

resource logs 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: 'log-${resourceToken}'
  location: location
  tags: tags
  properties: {
    retentionInDays: 30
    features: {
      searchVersion: 1
    }
    sku: {
      name: 'PerGB2018'
    }
  }
}

resource environment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: 'cae-${resourceToken}'
  location: location
  tags: tags
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logs.properties.customerId
        sharedKey: logs.listKeys().primarySharedKey
      }
    }
  }
}

resource identity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'mi-${resourceToken}'
  location: location
  tags: tags
}

resource registryPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(registry.id, identity.id, 'acrpull')
  scope: registry
  properties: {
    principalId: identity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId(
      'Microsoft.Authorization/roleDefinitions',
      '7f951dda-4ed3-4680-a7ca-43fe172d538d'
    )
  }
}

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' = if (isPostgres) {
  name: 'pg-${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Standard_B1ms'
    tier: 'Burstable'
  }
  properties: {
    administratorLogin: postgresUser
    administratorLoginPassword: databasePassword
    version: '16'
    storage: {
      storageSizeGB: 32
    }
    network: {
      publicNetworkAccess: 'Enabled'
    }
    backup: {
      backupRetentionDays: 7
      geoRedundantBackup: 'Disabled'
    }
    highAvailability: {
      mode: 'Disabled'
    }
  }
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-12-01-preview' = if (isPostgres) {
  parent: postgres
  name: postgresDatabase
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
}

var postgresHost = isPostgres ? postgres!.properties.fullyQualifiedDomainName : ''
var applicationEnvironment = concat([
  {
    name: 'MANAGEMENT_ENDPOINTS_WEB_EXPOSURE_INCLUDE'
    value: 'health'
  }
  {
    name: 'MANAGEMENT_ENDPOINT_HEALTH_PROBES_ENABLED'
    value: 'true'
  }
  {
    name: 'SPRING_H2_CONSOLE_ENABLED'
    value: 'false'
  }
], isPostgres ? [
  {
    name: 'MANAGEMENT_ENDPOINT_HEALTH_GROUP_READINESS_INCLUDE'
    value: 'readinessState,db'
  }
  {
    name: 'SPRING_PROFILES_ACTIVE'
    value: 'postgres'
  }
  {
    name: 'SPRING_DATASOURCE_URL'
    value: 'jdbc:postgresql://${postgresHost}:5432/${postgresDatabase}?sslmode=verify-full'
  }
  {
    name: 'SPRING_DATASOURCE_USERNAME'
    value: postgresUser
  }
  {
    name: 'SPRING_DATASOURCE_PASSWORD'
    secretRef: 'database-password'
  }
] : [])

resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: 'ca-${resourceToken}'
  location: location
  tags: union(tags, {
    'azd-service-name': 'petclinic'
  })
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${identity.id}': {}
    }
  }
  properties: {
    managedEnvironmentId: environment.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: isPlaceholder ? 80 : 8080
        transport: 'auto'
      }
      registries: [
        {
          server: registry.properties.loginServer
          identity: identity.id
        }
      ]
      secrets: isPostgres ? [
        {
          name: 'database-password'
          value: databasePassword
        }
      ] : []
    }
    template: {
      containers: [
        {
          name: 'petclinic'
          image: imageName
          env: isPlaceholder ? [] : applicationEnvironment
          resources: {
            cpu: json('1.0')
            memory: '2Gi'
          }
          probes: isPlaceholder ? [] : [
            {
              type: 'Liveness'
              httpGet: {
                path: '/actuator/health/liveness'
                port: 8080
                scheme: 'HTTP'
              }
              initialDelaySeconds: 90
              periodSeconds: 15
              timeoutSeconds: 5
              failureThreshold: 4
            }
            {
              type: 'Readiness'
              httpGet: {
                path: '/actuator/health/readiness'
                port: 8080
                scheme: 'HTTP'
              }
              initialDelaySeconds: 30
              periodSeconds: 10
              timeoutSeconds: 5
              failureThreshold: 12
            }
          ]
        }
      ]
      scale: {
        minReplicas: 1
        maxReplicas: 1
      }
    }
  }
  dependsOn: [
    registryPull
    database
  ]
}

output containerRegistryName string = registry.name
output containerRegistryEndpoint string = registry.properties.loginServer
output containerAppsEnvironmentName string = environment.name
output identityId string = identity.id
output containerAppId string = app.id
output containerAppName string = app.name
output postgresServerId string = isPostgres ? postgres!.id : ''
output postgresServerName string = isPostgres ? postgres!.name : ''
output postgresHost string = postgresHost
output postgresDatabase string = isPostgres ? postgresDatabase : ''
output postgresUser string = isPostgres ? postgresUser : ''
