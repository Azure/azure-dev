resource "azurerm_resource_group" "adjunct" {
  name     = var.resource_group_name
  location = var.location
  tags     = merge(var.tags, { "azd-env-name" = var.environment_name })
}
