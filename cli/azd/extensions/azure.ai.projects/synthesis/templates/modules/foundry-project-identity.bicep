targetScope = 'subscription'

param projectResourceId string
param projectEndpoint string

func matchesEndpoint(endpoint string, expected string) bool =>
  startsWith(endpoint, expected) ? empty(replace(substring(endpoint, length(expected)), '/', '')) : false

func matchesProjectEndpoint(endpoint string, subdomain string, projectName string) bool =>
  startsWith(toLower(trim(endpoint)), 'https://${toLower(subdomain)}.services.ai.azure.com/')
    ? !empty(filter(
        [
          'https://${toLower(subdomain)}.services.ai.azure.com/api/projects/${toLower(projectName)}'
          'https://${toLower(subdomain)}.services.ai.azure.com/projects/${toLower(projectName)}'
        ],
        expected => matchesEndpoint(toLower(trim(uriComponentToString(endpoint))), expected)
      ))
    : false

func endpointSubdomain(accountName string, customSubdomain string, advertisedEndpoint string) string =>
  toLower(!empty(customSubdomain)
    ? customSubdomain
    : (!empty(advertisedEndpoint) ? first(split(last(split(toLower(advertisedEndpoint), '://')), '.')) : accountName))

func validSubdomain(subdomain string) bool =>
  !contains(
    [
      length(subdomain) > 0
      length(subdomain) <= 63
      !startsWith(subdomain, '-')
      !endsWith(subdomain, '-')
      empty(filter(
        range(0, length(subdomain)),
        i => !contains('abcdefghijklmnopqrstuvwxyz0123456789-', substring(subdomain, i, 1))
      ))
    ],
    false
  )

@export()
func projectEndpointIdentityMatches(
  endpoint string,
  accountName string,
  projectName string,
  customSubdomain string,
  advertisedEndpoint string
) bool =>
  !contains(
    [
      validSubdomain(endpointSubdomain(accountName, customSubdomain, advertisedEndpoint))
      matchesProjectEndpoint(endpoint, endpointSubdomain(accountName, customSubdomain, advertisedEndpoint), projectName)
      empty(advertisedEndpoint) || matchesProjectEndpoint(
        advertisedEndpoint,
        endpointSubdomain(accountName, customSubdomain, advertisedEndpoint),
        projectName
      )
    ],
    false
  )

var projectIdParts = split(projectResourceId, '/')

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  scope: resourceGroup(projectIdParts[2], projectIdParts[4])
  name: projectIdParts[8]

  resource project 'projects' existing = {
    name: projectIdParts[10]
  }
}

var identityMatches = toLower(projectResourceId) == toLower(foundryAccount::project.id) && projectEndpointIdentityMatches(
  projectEndpoint,
  foundryAccount.name,
  projectIdParts[10],
  foundryAccount.properties.?customSubDomainName ?? '',
  foundryAccount::project.properties.?endpoints[?'AI Foundry API'] ?? ''
)

output valid bool = identityMatches
  ? true
  : fail('projectEndpoint must identify the same Foundry project as projectResourceId.')
