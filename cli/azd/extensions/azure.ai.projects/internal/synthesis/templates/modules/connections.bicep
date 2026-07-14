// Foundry project connections declared as host: azure.ai.connection services
// in azure.yaml. Creates one Microsoft.CognitiveServices/accounts/projects/connections
// resource per entry.
//
// This is the provision-time equivalent of the deploy-time azure.ai.connection
// service target, but it supports every auth type (the service target only
// upserts none/api-key/custom-keys at deploy). credentials and metadata are
// passed through untouched so any category/authType can be expressed.
//
// Pinned to 2025-04-01-preview via a separate existing account reference: GA
// 2025-06-01 fails to resolve the projects/connections sub-resource
// (MissingApiVersionParameter), the same reason acr.bicep does this.

// User-defined types

// Parameters

@description('Name of the existing Foundry CognitiveServices account that hosts the project.')
param foundryAccountName string

@description('Name of the existing Foundry project the connections are created on.')
param foundryProjectName string

@description('JSON-encoded connections to create on the Foundry project.')
@secure()
param connections string = ''

var connectionList = json(empty(connections) ? '[]' : connections)

// Resources

// Existing parent references so each connection nests under the project.
// Pinned to 2025-04-01-preview (see file header).
resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: foundryAccountName

  resource project 'projects' existing = {
    name: foundryProjectName
  }
}

// One connection per entry. Optional properties (credentials / metadata) are
// only emitted when supplied so None / identity-token connections don't send an
// empty credentials object.
resource projectConnections 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = [
  for c in connectionList: {
    parent: foundryAccount::project
    name: c.name
    properties: union(
      {
        category: c.category
        target: c.target
        authType: c.authType
      },
      c.?credentials != null ? { credentials: c.?credentials } : {},
      c.?metadata != null ? { metadata: c.?metadata } : {}
    )
  }
]

// Outputs

@description('Comma-joined names of the connections created, in input order. Reference these from toolbox tools via connection: <name>. A string (not an array) so it round-trips through the azd .env without JSON double-encoding.')
output connectionNames string = join(map(connectionList, c => c.name), ',')
