output "AZURE_COSMOS_CONNECTION_STRING_KEY" {
  value     = azurerm_cosmosdb_account.db.connection_strings[0]
  sensitive = true
}

output "AZURE_COSMOS_DATABASE_NAME" {
  value = azurerm_cosmosdb_mongo_database.mongodb.name
}

output "AZURE_KEY_VAULT_ENDPOINT" {
  value     = azurerm_key_vault.kv.vault_uri
  sensitive = true
}

output "REACT_APP_WEB_BASE_URL" {
  value = "https://${azurerm_linux_web_app.web.default_hostname}"
}

output "REACT_APP_API_BASE_URL" {
  value = "https://${azurerm_linux_web_app.api.default_hostname}"
}

output "AZURE_LOCATION" {
  value = var.location
}

output "APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  sensitive = true
}

output "REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  sensitive = true
}

