data "azapi_resource" "project" {
  type                   = "Microsoft.CognitiveServices/accounts/projects@2025-04-01-preview"
  resource_id            = local.normalized_project_id
  response_export_values = ["identity.principalId"]
}

locals {
  existing_acr_name    = element(reverse(split("/", var.existing_acr_resource_id)), 0)
  project_principal_id = data.azapi_resource.project.output.identity.principalId
}

resource "azapi_resource" "acr_connection" {
  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = "${local.existing_acr_name}-conn"
  parent_id = local.normalized_project_id

  body = {
    properties = {
      category = "ContainerRegistry"
      target   = var.existing_acr_endpoint
      authType = "ManagedIdentity"
      credentials = {
        clientId   = local.project_principal_id
        resourceId = var.existing_acr_resource_id
      }
      isSharedToAll = true
      metadata = {
        ResourceId = var.existing_acr_resource_id
      }
    }
  }
}
