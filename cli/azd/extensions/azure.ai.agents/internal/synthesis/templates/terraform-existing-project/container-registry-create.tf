locals {
  resource_token = substr(sha1(join("-", compact([
    var.subscription_id,
    var.resource_group_name,
    var.location,
    var.resource_token_salt,
  ]))), 0, 13)
  container_registry_name = "cr${local.resource_token}"
  acr_pull_role_id        = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
}

resource "azurerm_resource_group" "adjunct" {
  name     = var.resource_group_name
  location = var.location
  tags     = merge(var.tags, { "azd-env-name" = var.environment_name })
}

data "azapi_resource" "project" {
  type                   = "Microsoft.CognitiveServices/accounts/projects@2025-04-01-preview"
  resource_id            = local.normalized_project_id
  response_export_values = ["identity.principalId"]
}

locals {
  project_principal_id = data.azapi_resource.project.output.identity.principalId
}

resource "azurerm_container_registry" "this" {
  name                = local.container_registry_name
  resource_group_name = azurerm_resource_group.adjunct.name
  location            = azurerm_resource_group.adjunct.location
  tags                = var.tags
  sku                 = "Premium"
  admin_enabled       = false

  identity {
    type = "SystemAssigned"
  }

  public_network_access_enabled = true
  zone_redundancy_enabled       = false
}

resource "azurerm_role_assignment" "foundry_acr_pull" {
  scope              = azurerm_container_registry.this.id
  role_definition_id = "/subscriptions/${var.subscription_id}/providers/Microsoft.Authorization/roleDefinitions/${local.acr_pull_role_id}"
  principal_id       = local.project_principal_id
  principal_type     = "ServicePrincipal"
}

resource "azapi_resource" "acr_connection" {
  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = "${local.container_registry_name}-conn"
  parent_id = local.normalized_project_id

  body = {
    properties = {
      category = "ContainerRegistry"
      target   = azurerm_container_registry.this.login_server
      authType = "ManagedIdentity"
      credentials = {
        clientId   = local.project_principal_id
        resourceId = azurerm_container_registry.this.id
      }
      isSharedToAll = true
      metadata = {
        ResourceId = azurerm_container_registry.this.id
      }
    }
  }

  depends_on = [azurerm_role_assignment.foundry_acr_pull]
}
