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
      each.value.metadata != null ? { metadata = each.value.metadata } : {}
    )
  }

  sensitive_body = each.value.credentials != null ? {
    properties = {
      credentials = each.value.credentials
    }
  } : null
}
