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
      each.value.metadata != null ? { metadata = each.value.metadata } : {}
    )
  }

  sensitive_body = each.value.credentials != null ? {
    properties = {
      credentials = each.value.credentials
    }
  } : null
}
