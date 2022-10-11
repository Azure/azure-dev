#------------------------------------------------------------------------------------------------------
# Deploy resource Group
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "rg_name" {
  name          = var.environment_name
  resource_type = "azurerm_resource_group"
  random_length = 0
  clean_input   = true
}
resource "azurerm_resource_group" "rg" {
  name     = azurecaf_name.rg_name.result
  location = var.location

  tags = { azd-env-name : var.environment_name }
}

locals {
  tags             = { azd-env-name : var.environment_name }
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service plan
# ------------------------------------------------------------------------------------------------------
resource "random_string" "resource_token" {
  length  = 10
  upper   = false
  lower   = true
  numeric = false
  special = false
}

resource "azurecaf_name" "plan_name" {
  name          = random_string.resource_token.result
  resource_type = "azurerm_app_service_plan"
  random_length = 0
  clean_input   = true
}

resource "azurerm_service_plan" "plan" {
  name                = azurecaf_name.plan_name.result
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  os_type             = "Linux"
  sku_name            = "B1"

  tags = local.tags
}