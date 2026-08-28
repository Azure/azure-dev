resource "azapi_resource_action" "connection" {
  for_each = { for c in var.connections : c.name => c }

  type        = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  resource_id = "${local.normalized_project_id}/connections/${each.value.name}"
  method      = "PUT"

  body = {
    properties = merge(
      {
        category = each.value.category
        target   = each.value.target
        authType = each.value.authType
      },
      each.value.audience != null ? { audience = each.value.audience } : {},
      each.value.authorizationUrl != null ? { authorizationUrl = each.value.authorizationUrl } : {},
      each.value.tokenUrl != null ? { tokenUrl = each.value.tokenUrl } : {},
      each.value.refreshUrl != null ? { refreshUrl = each.value.refreshUrl } : {},
      each.value.scopes != null ? { scopes = each.value.scopes } : {},
      each.value.connectorName != null ? { connectorName = each.value.connectorName } : {},
      each.value.metadata != null ? { metadata = each.value.metadata } : {}
    )
  }

  sensitive_body = each.value.credentials != null ? {
    properties = {
      credentials = each.value.credentials
    }
  } : null
}
