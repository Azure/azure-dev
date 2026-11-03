@minLength(1)
param environmentName string

param location string = resourceGroup().location
param containerRegistryName string
param containerAppsEnvironmentName string
param identityId string
param identityClientId string
param containerAppName string
param imageName string

@secure()
@discriminator('type')
param databaseAuthentication {
  type: 'managedidentity'
  applicationPassword: ''
} | {
  type: 'password'
  @secure()
  @minLength(8)
  @maxLength(128)
  applicationPassword: string
}

@minLength(1)
@maxLength(63)
param databaseName string = 'petclinic'

@minLength(1)
@maxLength(63)
param databaseUsername string = 'petclinic_app'

@minLength(1)
param postgresHost string

var useManagedIdentity = databaseAuthentication.type == 'managedidentity'
var usePassword = databaseAuthentication.type == 'password'
var tags = {
  'azd-env-name': environmentName
  'azd-service-name': 'petclinic'
}
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
    value: identityClientId
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

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: containerRegistryName
}

resource environment 'Microsoft.App/managedEnvironments@2024-03-01' existing = {
  name: containerAppsEnvironmentName
}

resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: containerAppName
  location: location
  tags: tags
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${identityId}': {}
    }
  }
  properties: {
    managedEnvironmentId: environment.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
      }
      registries: [
        {
          server: registry.properties.loginServer
          identity: identityId
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
          env: applicationEnvironment
          resources: {
            cpu: json('1.0')
            memory: '2Gi'
          }
          probes: [
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
}

output SERVICE_PETCLINIC_URI string = 'https://${app.properties.configuration.ingress.fqdn}'
