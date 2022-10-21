terraform {
  required_providers {
    azurerm = {
      version = "~>3.18.0"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.15"
    }
  }
}

data "azurerm_client_config" "current" {}
# ------------------------------------------------------------------------------------------------------
# DEPLOY AZURE KEYVAULT
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "kv_name" {
  name          = var.resource_token
  resource_type = "azurerm_key_vault"
  random_length = 0
  clean_input   = true
}

resource "azurerm_key_vault" "kv" {
  name                     = azurecaf_name.kv_name.result
  location                 = var.location
  resource_group_name      = var.rg_name
  tenant_id                = data.azurerm_client_config.current.tenant_id
  purge_protection_enabled = false
  sku_name                 = "standard"

  tags = var.tags
}

resource "azurerm_key_vault_access_policy" "app" {
  count        = length(var.access_policy_object_ids)
  key_vault_id = azurerm_key_vault.kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = var.access_policy_object_ids[count.index]

  secret_permissions = [
    "Get",
    "Set",
    "List",
    "Delete",
  ]
}

resource "azurerm_key_vault_access_policy" "user" {
  count        = var.principal_id == "" ? 0 : 1
  key_vault_id = azurerm_key_vault.kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = var.principal_id

  secret_permissions = [
    "Get",
    "Set",
    "List",
    "Delete",
    "Purge"
  ]
}

resource "azurerm_key_vault_secret" "secrets" {
  count        = length(var.secrets)
  name         = var.secrets[count.index].name
  value        = var.secrets[count.index].value
  key_vault_id = azurerm_key_vault.kv.id
  depends_on = [
    azurerm_key_vault_access_policy.user,
    azurerm_key_vault_access_policy.app
  ]
}