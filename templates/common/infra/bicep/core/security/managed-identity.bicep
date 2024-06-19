metadata description = 'Creates an user-assigned managed identity.'
param name string
param location string = resourceGroup().location

resource apiIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: name
  location: location
}

output tenantId string = apiIdentity.properties.tenantId
output principalId string = apiIdentity.properties.principalId
output clientId string = apiIdentity.properties.clientId
