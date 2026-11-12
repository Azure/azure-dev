locals {
  project_id_parts        = split("/", var.project_resource_id)
  project_subscription_id = local.project_id_parts[2]
  project_resource_group  = local.project_id_parts[4]
  foundry_account_name    = local.project_id_parts[8]
  foundry_project_name    = local.project_id_parts[10]
  foundry_account_id      = join("/", slice(local.project_id_parts, 0, 9))
  normalized_project_id   = join("/", local.project_id_parts)
  project_endpoint_matches = regexall(
    "(?i)^https://([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\\.services\\.ai\\.azure\\.com/(?:api/)?projects/([^/?#]+)/?$",
    var.project_endpoint,
  )
  project_endpoint_account    = length(local.project_endpoint_matches) == 1 ? local.project_endpoint_matches[0][0] : ""
  project_endpoint_project    = length(local.project_endpoint_matches) == 1 ? local.project_endpoint_matches[0][1] : ""
  advertised_project_endpoint = try(data.azapi_resource.project.output.properties.endpoints["AI Foundry API"], null)
  foundry_account_subdomain = coalesce(
    try(data.azapi_resource.foundry_account.output.properties.customSubDomainName, null),
    try(regex("(?i)^https://([^.]+)\\.services\\.ai\\.azure\\.com/", local.advertised_project_endpoint)[0], null),
    local.foundry_account_name,
  )
  foundry_account_subdomain_valid = length(regexall(
    "(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$", local.foundry_account_subdomain,
  )) == 1
  expected_project_endpoint = coalesce(
    local.advertised_project_endpoint,
    "https://${local.foundry_account_subdomain}.services.ai.azure.com/api/projects/${local.foundry_project_name}",
  )
  expected_endpoint_matches = regexall(
    "(?i)^https://([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\\.services\\.ai\\.azure\\.com/(?:api/)?projects/([^/?#]+)/?$",
    local.expected_project_endpoint,
  )
  expected_endpoint_account = length(local.expected_endpoint_matches) == 1 ? local.expected_endpoint_matches[0][0] : ""
  expected_endpoint_project = length(local.expected_endpoint_matches) == 1 ? local.expected_endpoint_matches[0][1] : ""
}

data "azapi_resource" "foundry_account" {
  type                   = "Microsoft.CognitiveServices/accounts@2025-04-01-preview"
  resource_id            = local.foundry_account_id
  response_export_values = ["properties.customSubDomainName"]
}

data "azapi_resource" "project" {
  type                   = "Microsoft.CognitiveServices/accounts/projects@2025-04-01-preview"
  resource_id            = local.normalized_project_id
  response_export_values = ["properties.endpoints", "identity.principalId"]
}
