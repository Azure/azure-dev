targetScope = 'resourceGroup'

type deploymentType = {
  name: string
  model: {
    name: string
    format: string
    version: string
  }
  sku: {
    name: string
    capacity: int
  }
}

type connectionType = {
  name: string
  category: string
  target: string
  authType: string
  metadata: object?
}

param accountName string
param projectName string
param deployments deploymentType[] = []
param connections connectionType[] = []
@secure()
param connectionCredentials object = {}
param acrName string = ''
param acrEndpoint string = ''
param acrResourceId string = ''
param createAcrConnection bool = false
param existingAcrConnectionName string = ''

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' existing = {
  name: accountName
}

@batchSize(1)
resource modelDeployments 'Microsoft.CognitiveServices/accounts/deployments@2025-06-01' = [
  for d in deployments: {
    parent: foundryAccount
    name: d.name
    properties: {
      model: d.model
    }
    sku: d.sku
  }
]

resource foundryAccountPreview 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: accountName

  resource project 'projects' existing = {
    name: projectName
  }
}

resource acrConnection 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = if (createAcrConnection) {
  parent: foundryAccountPreview::project
  name: '${acrName}-conn'
  properties: {
    category: 'ContainerRegistry'
    target: acrEndpoint
    authType: 'ManagedIdentity'
    credentials: {
      clientId: foundryAccountPreview::project.identity.principalId
      resourceId: acrResourceId
    }
    isSharedToAll: true
    metadata: {
      ResourceId: acrResourceId
    }
  }
}

resource projectConnections 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = [
  for c in connections: {
    parent: foundryAccountPreview::project
    name: c.name
    properties: union(
      {
        category: c.category
        target: c.target
        authType: c.authType
      },
      contains(connectionCredentials, c.name) ? { credentials: connectionCredentials[c.name] } : {},
      c.?metadata != null ? { metadata: c.?metadata } : {}
    )
  }
]

output acrConnectionName string = createAcrConnection ? acrConnection!.name : existingAcrConnectionName
output connectionNames string = join(map(connections, c => c.name), ',')
