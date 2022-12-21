output "SERVICE_API_URI" {
  value = "${azurerm_api_management.myapim.gatewayUrl}/${var.apiPath}"
}