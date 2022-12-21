output "SERVICE_API_URI" {
  value = "${data.azurerm_api_management.myapim.gateway_url}/${var.apiPath}"
}