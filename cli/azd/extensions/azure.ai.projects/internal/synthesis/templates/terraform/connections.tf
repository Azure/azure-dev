# Foundry project connections declared as host: azure.ai.connection services
# in azure.yaml. One azapi_resource per entry.
#
# Provision-time equivalent of the deploy-time azure.ai.connection service
# target, but supports every auth type (the service target only upserts
# none/api-key/custom-keys). credentials/metadata pass through untouched.
#
# Pinned to 2025-04-01-preview: GA 2025-06-01 cannot resolve the
# projects/connections sub-resource (MissingApiVersionParameter), same as
# acr.tf's acr_connection.
resource "azapi_resource" "connection" {
  for_each = { for c in var.connections : c.name => c }

  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = each.value.name
  parent_id = azapi_resource.project.id

  body = {
    properties = merge(
      {
        category = each.value.category
        target   = each.value.target
        authType = each.value.authType
      },
      each.value.audience != null && each.value.audience != "" ? { audience = each.value.audience } : {},
      each.value.authorizationUrl != null ? { authorizationUrl = each.value.authorizationUrl } : {},
      each.value.tokenUrl != null ? { tokenUrl = each.value.tokenUrl } : {},
      each.value.refreshUrl != null ? { refreshUrl = each.value.refreshUrl } : {},
      each.value.scopes != null ? { scopes = each.value.scopes } : {},
      each.value.connectorName != null && each.value.connectorName != "" ? { connectorName = each.value.connectorName } : {},
      each.value.metadata != null ? { metadata = each.value.metadata } : {}
    )
  }

  sensitive_body = each.value.credentials != null || lower(each.value.authType) == "oauth2" ? {
    properties = {
      credentials = each.value.credentials != null ? each.value.credentials : {}
    }
  } : null
}
