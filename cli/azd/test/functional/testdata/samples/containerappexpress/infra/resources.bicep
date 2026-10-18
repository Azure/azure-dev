param environmentName string
param location string = resourceGroup().location

var tags = {
  'azd-env-name': environmentName
}
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2026-07-01' = {
  name: 'cae-${resourceToken}'
  location: location
  tags: tags
  properties: {
    environmentMode: 'Express'
  }
}

resource containerApp 'Microsoft.App/containerApps@2026-07-01' = {
  name: 'web'
  location: location
  tags: union(tags, {
    'azd-service-name': 'web'
  })
  properties: {
    environmentId: containerAppsEnvironment.id
    configuration: {
      ingress: {
        external: true
        targetPort: 80
        transport: 'http'
      }
    }
    template: {
      containers: [
        {
          name: 'web'
          image: 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
          resources: {
            cpu: json('0.25')
            memory: '0.5Gi'
          }
        }
      ]
      scale: {
        minReplicas: 0
        maxReplicas: 1
      }
    }
  }
}

output containerAppsEnvironmentName string = containerAppsEnvironment.name
output websiteUrl string = 'https://${containerApp.properties.configuration.ingress.fqdn}'
