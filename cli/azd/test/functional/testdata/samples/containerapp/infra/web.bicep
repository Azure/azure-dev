param environmentName string
param location string = resourceGroup().location
param imageName string
param containerRegistryName string
param containerAppsEnvironmentName string
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource containerRegistry 'Microsoft.ContainerRegistry/registries@2023-01-01-preview' existing = {
  name: containerRegistryName
}

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: containerAppsEnvironmentName
}

resource app 'Microsoft.App/containerApps@2023-05-02-preview' = {
  name: 'ca-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'web' })
  properties: {
    managedEnvironmentId: containerAppsEnvironment.id
    configuration: {
      activeRevisionsMode: 'single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
      }
      secrets: [
        {
          name: 'registry-password'
          value: containerRegistry.listCredentials().passwords[0].value
        }
      ]
      registries: [
        {
          server: '${containerRegistry.name}.azurecr.io'
          username: containerRegistry.name
          passwordSecretRef: 'registry-password'
        }
      ]
    }
    template: {
      containers: [
        {
          image: imageName
          name: 'main'
          env: []
        }
      ]
    }
  }
}

output WEBSITE_URL string = 'https://${app.properties.configuration.ingress.fqdn}'
