# ------------------------------------------------------------------------------------------------------
# Deploy app service plan
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "plan_name" {
  name          = local.resource_token
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
  name          = "web-${local.resource_token}"
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
    always_on        = true
    ftps_state       = "FtpsOnly"
    app_command_line = "pm2 serve /home/site/wwwroot --no-daemon --spa"
    application_stack {
      node_version = "16-lts"
    }
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
  name          = "api-${local.resource_token}"
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
    always_on        = true
    ftps_state       = "FtpsOnly"
    app_command_line = local.api_command_line
    application_stack {
      python_version = "3.8"
    }
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
