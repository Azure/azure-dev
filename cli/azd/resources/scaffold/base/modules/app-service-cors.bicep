@description('The name of the source application that will make requests')
param sourceAppName string

@description('The name of the target application that will receive requests')
param targetAppName string

resource sourceApp 'Microsoft.Web/sites@2024-04-01' existing = {
  name: sourceAppName
}

resource targetApp 'Microsoft.Web/sites@2024-04-01' existing = {
  name: targetAppName

  resource corsSettings 'config' = {
    name: 'web'
    properties: {
      cors: {
        allowedOrigins: [
          'https://portal.azure.com'
          'https://ms.portal.azure.com'
          'https://${sourceApp.properties.defaultHostName}'
        ]
      }
    }
  }
}
