terraform {
  required_providers {
    azurerm = {
      version = "~>3.47.0"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.24"
    }
  }
}
# ------------------------------------------------------------------------------------------------------
# Deploy PostgreSQL Server
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "psql_name" {
  name          = var.resource_token
  resource_type = "azurerm_postgresql_flexible_server"
  random_length = 0
  clean_input   = true
}

data "azurerm_client_config" "current" {}

locals {
  tenant_id       = var.tenant_id == "" ? data.azurerm_client_config.current.tenant_id : var.tenant_id
  object_id       = var.object_id == "" ? data.azurerm_client_config.current.object_id : var.object_id
  principal_name  = var.principal_name == "" ? data.azurerm_client_config.current.object_id : var.principal_name
  principal_type  = var.client_id == "" ? "User" : "ServicePrincipal"
}

resource "random_password" "password" {
  length           = 32
  special          = true
  override_special = "_%@"
}

resource "azurerm_postgresql_flexible_server" "psql_server" {
  name                            = azurecaf_name.psql_name.result
  location                        = var.location
  resource_group_name             = var.rg_name
  tags                            = var.tags
  version                         = "12"
  administrator_login             = var.administrator_login
  administrator_password          = random_password.password.result
  zone                            = "1"

  storage_mb                      = 32768

  sku_name                        = "GP_Standard_D4s_v3"

  authentication {
    active_directory_auth_enabled = true
    password_auth_enabled         = true
    tenant_id                     = data.azurerm_client_config.current.tenant_id
  }
}


resource "azurerm_postgresql_flexible_server_firewall_rule" "firewall_rule" {
  name                            = "AllowAllFireWallRule"
  server_id                       = azurerm_postgresql_flexible_server.psql_server.id
  start_ip_address                = "0.0.0.0"
  end_ip_address                  = "255.255.255.255"
}

resource "azurerm_postgresql_flexible_server_database" "database" {
  name      = var.database_name
  server_id = azurerm_postgresql_flexible_server.psql_server.id
  collation = "en_US.utf8"
  charset   = "utf8"
}

resource "azurerm_postgresql_flexible_server_active_directory_administrator" "aad_admin" {
  server_name         = azurerm_postgresql_flexible_server.psql_server.name
  resource_group_name = var.rg_name
  tenant_id           = local.tenant_id
  object_id           = local.object_id
  principal_name      = local.principal_name
  principal_type      = local.principal_type
}