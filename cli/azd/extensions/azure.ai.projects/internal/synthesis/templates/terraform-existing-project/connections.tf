resource "azapi_resource" "connection" {
  for_each = { for c in var.connections : c.name => c }

  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = each.value.name
  parent_id = local.normalized_project_id

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
