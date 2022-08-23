locals {
  tags             = { azd-env-name : var.name }
  api_command_line = "gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile \"-\" --error-logfile \"-\" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app"
}
# ------------------------------------------------------------------------------------------------------
# Deploy resource Group
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "rg_name" {
  name          = var.name
  resource_type = "azurerm_resource_group"
  random_length = 0
  clean_input   = true
}
resource "azurerm_resource_group" "rg" {
  name     = azurecaf_name.rg_name.result
  location = var.location

  tags = local.tags
}

resource "random_string" "resource_token" {
  length  = 10
  upper   = false
  lower   = true
  numeric = false
  special = false
}

module "applicationinsights" {
  source         = "../../../../../../common/infra/terraform"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  env_name       = var.name
  workspace_id   = azurerm_log_analytics_workspace.workspace.id
  tags           = azurerm_resource_group.rg.tags
  resource_token = random_string.resource_token.result

}

# ------------------------------------------------------------------------------------------------------
# DEPLOY AZURE KEYVAULT
# ------------------------------------------------------------------------------------------------------

resource "azurecaf_name" "kv_name" {
  name          = random_string.resource_token.result #revert me
  resource_type = "azurerm_key_vault"
  random_length = 0
  clean_input   = true
}

resource "azurerm_key_vault" "kv" {
  name                     = azurecaf_name.kv_name.result
  location                 = azurerm_resource_group.rg.location
  resource_group_name      = azurerm_resource_group.rg.name
  tenant_id                = data.azurerm_client_config.current.tenant_id
  purge_protection_enabled = false
  sku_name                 = "standard"

  tags = local.tags
}

resource "azurerm_key_vault_access_policy" "app" {
  key_vault_id = azurerm_key_vault.kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = azurerm_linux_web_app.api.identity.0.principal_id

  secret_permissions = [
    "Get",
    "Set",
    "List",
    "Delete",
  ]
}

resource "azurerm_key_vault_access_policy" "user" {
  count        = var.principalId == "" ? 0 : 1
  key_vault_id = azurerm_key_vault.kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = var.principalId

  secret_permissions = [
    "Get",
    "Set",
    "List",
    "Delete",

  ]
}

resource "azurerm_key_vault_secret" "dbconnection" {
  name         = "AZURE-COSMOS-CONNECTION-STRING"
  value        = azurerm_cosmosdb_account.db.connection_strings[0]
  key_vault_id = azurerm_key_vault.kv.id

}
# ------------------------------------------------------------------------------------------------------
# Deploy app service plan
# ------------------------------------------------------------------------------------------------------
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

# ------------------------------------------------------------------------------------------------------
# Deploy app service web app
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "web_name" {
  name          = "web-${random_string.resource_token.result}"
  resource_type = "azurerm_app_service"
  random_length = 0
  clean_input   = true
}
resource "azurerm_linux_web_app" "web" {
  name                = azurecaf_name.web_name.result
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  service_plan_id     = azurerm_service_plan.plan.id
  https_only          = true
  tags                = merge(local.tags, { azd-service-name : "web" })

  site_config {
    #linux_fx_version = "NODE|16-lts"
    always_on        = true
    ftps_state       = "FtpsOnly"
    app_command_line = "pm2 serve /home/site/wwwroot --no-daemon --spa"
  }


  app_settings = {
    "SCM_DO_BUILD_DURING_DEPLOYMENT"        = "false"
    "APPLICATIONINSIGHTS_CONNECTION_STRING" = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  }

  logs {
    application_logs {
      file_system_level = "Verbose"
    }
    detailed_error_messages = true
    failed_request_tracing  = true
    http_logs {
      file_system {
        retention_in_days = 1
        retention_in_mb   = 35
      }
    }
  }

}

# ------------------------------------------------------------------------------------------------------
# Deploy app service api
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "appi_name" {
  name          = "api-${random_string.resource_token.result}"
  resource_type = "azurerm_app_service"
  random_length = 0
  clean_input   = true
}


resource "azurerm_linux_web_app" "api" {
  name                = azurecaf_name.appi_name.result
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  service_plan_id     = azurerm_service_plan.plan.id
  https_only          = true
  tags                = merge(local.tags, { "azd-service-name" : "api" })

  site_config {
    #linux_fx_version = "PYTHON|3.8"
    always_on        = true
    ftps_state       = "FtpsOnly"
    app_command_line = local.api_command_line
  }

  identity {
    type = "SystemAssigned"
  }

  app_settings = {
    "AZURE_COSMOS_CONNECTION_STRING_KEY"    = "AZURE-COSMOS-CONNECTION-STRING"
    "AZURE_COSMOS_DATABASE_NAME"            = azurerm_cosmosdb_mongo_database.mongodb.name
    "SCM_DO_BUILD_DURING_DEPLOYMENT"        = "true"
    "AZURE_KEY_VAULT_ENDPOINT"              = azurerm_key_vault.kv.vault_uri
    "APPLICATIONINSIGHTS_CONNECTION_STRING" = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  }
  logs {
    application_logs {
      file_system_level = "Verbose"
    }
    detailed_error_messages = true
    failed_request_tracing  = true
    http_logs {
      file_system {
        retention_in_days = 1
        retention_in_mb   = 35
      }
    }
  }
}

# ------------------------------------------------------------------------------------------------------
# Deploy log analytics workspace
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "workspace_name" {
  name          = random_string.resource_token.result
  resource_type = "azurerm_log_analytics_workspace"
  random_length = 0
  clean_input   = true
}

resource "azurerm_log_analytics_workspace" "workspace" {
  name                = azurecaf_name.workspace_name.result
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
  tags                = local.tags
}

# ------------------------------------------------------------------------------------------------------
# Deploy cosmos db account
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "db_acc_name" {
  name          = random_string.resource_token.result
  resource_type = "azurerm_cosmosdb_account"
  random_length = 0
  clean_input   = true
}

resource "azurerm_cosmosdb_account" "db" {
  name                            = azurecaf_name.db_acc_name.result
  location                        = azurerm_resource_group.rg.location
  resource_group_name             = azurerm_resource_group.rg.name
  offer_type                      = "Standard"
  kind                            = "MongoDB"
  enable_automatic_failover       = false
  enable_multiple_write_locations = false
  mongo_server_version            = "4.0"

  tags = local.tags


  capabilities {
    name = "EnableServerless"
  }

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = azurerm_resource_group.rg.location
    failover_priority = 0
    zone_redundant    = false
  }

}

# ------------------------------------------------------------------------------------------------------
# Deploy cosmos mongo db and collections
# ------------------------------------------------------------------------------------------------------
resource "azurerm_cosmosdb_mongo_database" "mongodb" {
  name                = "Todo"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
}

resource "azurerm_cosmosdb_mongo_collection" "todolist" {
  name                = "TodoList"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
  database_name       = azurerm_cosmosdb_mongo_database.mongodb.name
  shard_key           = "_id"


  index {
    keys   = ["_id"]
    unique = true
  }
}

resource "azurerm_cosmosdb_mongo_collection" "todocollection" {
  name                = "TodoItem"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
  database_name       = azurerm_cosmosdb_mongo_database.mongodb.name
  shard_key           = "_id"


  index {
    keys   = ["_id"]
    unique = true
  }
}