output "APIM_SERVICE_NAME" {
  value = azurerm_api_management.apim.name
}

output "API_MANAGEMENT_LOGGER_ID" {
  value = azurerm_api_management_logger.logger.id
}
