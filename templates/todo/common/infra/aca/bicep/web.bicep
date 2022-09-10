@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param name string

@minLength(1)
@description('Primary location for all resources')
param location string

param imageName string

var resourceToken = toLower(uniqueString(subscription().id, name, location))
var tags = { 'azd-env-name': name }
var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: '${abbrs.appManagedEnvironments}${resourceToken}'
}

// 2022-02-01-preview needed for anonymousPullEnabled
resource containerRegistry 'Microsoft.ContainerRegistry/registries@2022-02-01-preview' existing = {
  name: '${abbrs.containerRegistryRegistries}${resourceToken}'
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource api 'Microsoft.App/containerApps@2022-03-01' existing = {
  name: '${abbrs.appContainerApps}api-${resourceToken}'
}

resource web 'Microsoft.App/containerApps@2022-03-01' = {
  name: '${abbrs.appContainerApps}web-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'web' })
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    managedEnvironmentId: containerAppsEnvironment.id
    configuration: {
      activeRevisionsMode: 'single'
      ingress: {
        external: true
        targetPort: 80
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
          env: [
            {
              name: 'REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING'
              value: applicationInsights.properties.ConnectionString
            }
            {
              name: 'REACT_APP_API_BASE_URL'
              value: 'https://${api.properties.configuration.ingress.fqdn}'
            }
          ]
        }
      ]
    }
  }
}

output WEB_URI string = 'https://${web.properties.configuration.ingress.fqdn}'
