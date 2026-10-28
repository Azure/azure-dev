@minLength(1)
param environmentName string

param location string = resourceGroup().location
param containerRegistryName string
param containerAppsEnvironmentName string
param identityId string
param containerAppName string
param imageName string

@allowed([
  'h2'
  'postgresql'
])
param databaseType string = 'h2'

@secure()
param databasePassword string = ''

param postgresHost string = ''
param postgresDatabase string = 'petclinic'
param postgresUser string = 'petclinicadmin'

var isPostgres = databaseType == 'postgresql'
var tags = {
  'azd-env-name': environmentName
  'azd-service-name': 'petclinic'
}

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
          env: concat([
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
}

output SERVICE_PETCLINIC_URI string = 'https://${app.properties.configuration.ingress.fqdn}'
