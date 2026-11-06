data "azapi_resource" "project" {
  type                   = "Microsoft.CognitiveServices/accounts/projects@2025-04-01-preview"
  resource_id            = local.normalized_project_id
  response_export_values = ["identity.principalId"]
}

provider "azurerm" {
  alias           = "existing_acr"
  subscription_id = split("/", var.existing_acr_resource_id)[2]
  tenant_id       = var.tenant_id
  features {}
}

locals {
  existing_acr_name    = element(reverse(split("/", var.existing_acr_resource_id)), 0)
  project_principal_id = data.azapi_resource.project.output.identity.principalId
  acr_pull_role_id     = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
}

resource "azurerm_role_assignment" "foundry_acr_pull" {
  provider           = azurerm.existing_acr
  scope              = var.existing_acr_resource_id
  role_definition_id = "/subscriptions/${split("/", var.existing_acr_resource_id)[2]}/providers/Microsoft.Authorization/roleDefinitions/${local.acr_pull_role_id}"
  principal_id       = local.project_principal_id
  principal_type     = "ServicePrincipal"
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

  depends_on = [azurerm_role_assignment.foundry_acr_pull]
}
