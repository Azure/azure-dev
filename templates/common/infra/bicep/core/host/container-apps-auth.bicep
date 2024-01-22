metadata description = 'Creates an Azure Container Apps Auth Config using Microsoft Entra as Identity Provider.'

@description('The name of the container apps resource within the current resource group scope')
param name string

@description('The client ID of the Microsoft Entra application.')
param clientId string

@description('The name of the Container Apps secret that contains the client secret of the Microsoft Entra application.')
param clientSecretName string

@description('The OpenID issuer of the Microsoft Entra application.')
param openIdIssuer string


resource app 'Microsoft.App/containerApps@2023-05-01' existing = {
  name: name
}

resource auth 'Microsoft.App/containerApps/authConfigs@2023-05-01' = {
  parent: app
  name: 'current'
  properties: {
    platform: {
      enabled: true
    }
    globalValidation: {
      redirectToProvider: 'azureactivedirectory'
      unauthenticatedClientAction: 'RedirectToLoginPage'
    }
    identityProviders: {
      azureActiveDirectory: {
        registration: {
          clientId: clientId
          clientSecretSettingName: clientSecretName
          openIdIssuer: openIdIssuer
        }
      }
    }
  }
}

