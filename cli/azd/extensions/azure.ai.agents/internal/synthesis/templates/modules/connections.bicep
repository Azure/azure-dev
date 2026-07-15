// Foundry project connections declared as host: azure.ai.connection services
// in azure.yaml. Creates one Microsoft.CognitiveServices/accounts/projects/connections
// resource per entry.
//
// Provision-time equivalent of the deploy-time connection target.
// Supports every auth type. Metadata passes through.
// Credentials arrive in a separate secure parameter.
//
// Pinned to 2025-04-01-preview via a separate existing account reference: GA
// 2025-06-01 fails to resolve the projects/connections sub-resource
// (MissingApiVersionParameter), the same reason acr.bicep does this.

// User-defined types

@description('Shape of one Foundry project connection (a host: azure.ai.connection service).')
type connectionType = {
  @description('Connection name. The resource name and the key a toolbox tool references via connection: <name>.')
  name: string

  @description('Connection category, e.g. RemoteTool (MCP), CognitiveSearch, AzureOpenAI, ApiKey, CustomKeys.')
  category: string

  @description('Target endpoint URL or ARM resource id. For a RemoteTool/MCP connection this is the MCP server URL.')
  target: string

  @description('Auth type: None | ApiKey | CustomKeys | OAuth2 | UserEntraToken | ProjectManagedIdentity | AgenticIdentityToken | ManagedIdentity | ...')
  authType: string

  @description('Optional metadata key-value pairs.')
  metadata: object?
}

@description('Shape of a list of connections.')
type connectionsType = connectionType[]

// Parameters

@description('Name of the existing Foundry CognitiveServices account that hosts the project.')
param foundryAccountName string

@description('Name of the existing Foundry project the connections are created on.')
param foundryProjectName string

@description('Connections to create on the Foundry project. Each entry maps to one host: azure.ai.connection service.')
param connections connectionsType = []

@description('Credentials keyed by Foundry project connection name.')
@secure()
param connectionCredentials object = {}

// Resources

// Existing parent references so each connection nests under the project.
// Pinned to 2025-04-01-preview (see file header).
resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: foundryAccountName

  resource project 'projects' existing = {
    name: foundryProjectName
  }
}

// Optional credentials and metadata are emitted only when supplied.
resource projectConnections 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = [
  for c in connections: {
    parent: foundryAccount::project
    name: c.name
    properties: union(
      {
        category: c.category
        target: c.target
        authType: c.authType
      },
      contains(connectionCredentials, c.name)
        ? { credentials: connectionCredentials[c.name] }
        : {},
      c.?metadata != null ? { metadata: c.?metadata } : {}
    )
  }
]

// Outputs

@description('Comma-joined names of the connections created, in input order. Reference these from toolbox tools via connection: <name>. A string (not an array) so it round-trips through the azd .env without JSON double-encoding.')
output connectionNames string = join(map(connections, c => c.name), ',')
