param environmentName string
param location string = resourceGroup().location

@secure()
@discriminator('type')
param databaseAuthentication {
  type: 'managedidentity'
  @minLength(1)
  administratorName: string
  @minLength(1)
  administratorPrincipalId: string
  administratorPassword: ''
  applicationPassword: ''
} | {
  type: 'password'
  administratorName: '' | 'petclinicadmin'
  administratorPrincipalId: ''
  @secure()
  @minLength(8)
  @maxLength(128)
  administratorPassword: string
  @secure()
  @minLength(8)
  @maxLength(128)
  applicationPassword: string
}

@minLength(1)
@maxLength(63)
param databaseName string

@minLength(1)
@maxLength(63)
param databaseUsername string

param imageName string

var useManagedIdentity = databaseAuthentication.type == 'managedidentity'
var usePassword = databaseAuthentication.type == 'password'
var placeholderImage = 'mcr.microsoft.com/k8se/quickstart:latest'
var isPlaceholder = imageName == placeholderImage
var resourceToken = toLower(uniqueString(subscription().id, resourceGroup().id, environmentName))
var tags = {
  'azd-env-name': environmentName
}
var postgresAdministrator = usePassword && empty(databaseAuthentication.administratorName)
  ? 'petclinicadmin'
  : databaseAuthentication.administratorName

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

resource virtualNetwork 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: 'vnet-${resourceToken}'
  location: location
  tags: tags
  properties: {
    addressSpace: {
      addressPrefixes: [
        '10.42.0.0/16'
      ]
    }
  }
}

resource networkSecurityGroup 'Microsoft.Network/networkSecurityGroups@2024-05-01' = {
  name: 'nsg-${resourceToken}'
  location: location
  tags: tags
  properties: {
    securityRules: [
      {
        name: 'AllowHttpHttpsInbound'
        properties: {
          priority: 100
          protocol: 'Tcp'
          access: 'Allow'
          direction: 'Inbound'
          sourcePortRange: '*'
          sourceAddressPrefix: 'Internet'
          destinationAddressPrefix: '*'
          destinationPortRanges: [
            '80'
            '443'
          ]
        }
      }
    ]
  }
}

resource acaInfrastructureSubnet 'Microsoft.Network/virtualNetworks/subnets@2024-05-01' = {
  parent: virtualNetwork
  name: 'aca-infrastructure'
  properties: {
    addressPrefix: '10.42.0.0/23'
    defaultOutboundAccess: false
    networkSecurityGroup: {
      id: networkSecurityGroup.id
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
    vnetConfiguration: {
      infrastructureSubnetId: acaInfrastructureSubnet.id
      internal: false
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

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: 'pg-${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Standard_B1ms'
    tier: 'Burstable'
  }
  properties: union({
    authConfig: {
      activeDirectoryAuth: useManagedIdentity ? 'Enabled' : 'Disabled'
      passwordAuth: usePassword ? 'Enabled' : 'Disabled'
      tenantId: tenant().tenantId
    }
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
  }, usePassword ? {
    administratorLogin: postgresAdministrator
    administratorLoginPassword: databaseAuthentication.administratorPassword
  } : {})
}

resource entraAdministrator 'Microsoft.DBforPostgreSQL/flexibleServers/administrators@2024-08-01' = if (useManagedIdentity) {
  parent: postgres
  name: databaseAuthentication.administratorPrincipalId
  properties: {
    principalName: databaseAuthentication.administratorName
    principalType: 'User'
    tenantId: tenant().tenantId
  }
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: postgres
  name: databaseName
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
}

var postgresHost = postgres.properties.fullyQualifiedDomainName
var postgresJdbcBase = 'jdbc:postgresql://${postgresHost}:5432/${databaseName}?sslmode=verify-full&sslfactory=org.postgresql.ssl.DefaultJavaSSLFactory'
var postgresJdbcManagedIdentity = '${postgresJdbcBase}&authenticationPluginClassName=com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin'
var commonApplicationEnvironment = [
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
  {
    name: 'MANAGEMENT_ENDPOINT_HEALTH_GROUP_READINESS_INCLUDE'
    value: 'readinessState,db'
  }
  {
    name: 'SPRING_PROFILES_ACTIVE'
    value: 'postgres'
  }
  {
    name: 'SPRING_SQL_INIT_MODE'
    value: 'always'
  }
  {
    name: 'SPRING_JPA_HIBERNATE_DDL_AUTO'
    value: 'none'
  }
  {
    name: 'SPRING_DATASOURCE_URL'
    value: useManagedIdentity ? postgresJdbcManagedIdentity : postgresJdbcBase
  }
  {
    name: 'SPRING_DATASOURCE_USERNAME'
    value: databaseUsername
  }
]
var managedIdentityApplicationEnvironment = [
  {
    name: 'AZURE_CLIENT_ID'
    value: identity.properties.clientId
  }
]
var passwordApplicationEnvironment = [
  {
    name: 'SPRING_DATASOURCE_PASSWORD'
    secretRef: 'database-app-password'
  }
]
var applicationEnvironment = concat(
  commonApplicationEnvironment,
  useManagedIdentity ? managedIdentityApplicationEnvironment : passwordApplicationEnvironment
)

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
      secrets: usePassword ? [
        {
          name: 'database-app-password'
          // The password branch marks this property secure; the managed identity branch is the empty literal.
          #disable-next-line use-secure-value-for-secure-inputs
          value: databaseAuthentication.applicationPassword
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
              initialDelaySeconds: 60
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
    entraAdministrator
  ]
}

output containerRegistryName string = registry.name
output containerRegistryEndpoint string = registry.properties.loginServer
output containerAppsEnvironmentName string = environment.name
output identityId string = identity.id
output identityClientId string = identity.properties.clientId
output identityPrincipalId string = identity.properties.principalId
output containerAppId string = app.id
output containerAppName string = app.name
output postgresServerId string = postgres.id
output postgresServerName string = postgres.name
output postgresHost string = postgresHost
output postgresDatabase string = database.name
output postgresUser string = databaseUsername
output postgresAdministrator string = postgresAdministrator
