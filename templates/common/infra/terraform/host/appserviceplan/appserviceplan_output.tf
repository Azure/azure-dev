output "APPSERVICE_PLAN_ID" {
  value     = azurerm_service_plan.plan.id
  sensitive = true
}