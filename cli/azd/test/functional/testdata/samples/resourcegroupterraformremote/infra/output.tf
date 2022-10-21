output "AZURE_LOCATION" {
  value = var.location
}

output "RG_NAME" {
  value = azurerm_resource_group.rg.name
}
output "APP_SVC_NAME" {
  value = azurerm_service_plan.plan.name
}