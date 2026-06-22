# Azure Container Registry for hosted agents that use docker, wired as a
# connection on the Foundry project so the project identity can pull images.
# Premium SKU is required for Foundry registry connections.

locals {
  container_registry_name = "cr${local.resource_token}"

  # https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
  acr_pull_role_id = "7f951dda-4ed3-4680-a7ca-43fe172d538d"

  project_principal_id = try(azapi_resource.project.output.identity.principalId, "")
}

resource "azurerm_container_registry" "this" {
  name                = local.container_registry_name
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  tags                = var.tags
  sku                 = "Premium"
  admin_enabled       = false

  identity {
    type = "SystemAssigned"
  }

  public_network_access_enabled = true
  zone_redundancy_enabled       = false
}

# Grants the Foundry project identity AcrPull so hosted agents can pull images.
resource "azurerm_role_assignment" "foundry_acr_pull" {
  scope              = azurerm_container_registry.this.id
  role_definition_id = "/subscriptions/${var.subscription_id}/providers/Microsoft.Authorization/roleDefinitions/${local.acr_pull_role_id}"
  principal_id       = local.project_principal_id
  principal_type     = "ServicePrincipal"
}

# Project connection so Foundry can resolve the registry by name.
# Pinned to 2025-04-01-preview: GA 2025-06-01 cannot resolve the
# projects/connections ContainerRegistry sub-resource (MissingApiVersionParameter).
resource "azapi_resource" "acr_connection" {
  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = "${local.container_registry_name}-conn"
  parent_id = azapi_resource.project.id

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
