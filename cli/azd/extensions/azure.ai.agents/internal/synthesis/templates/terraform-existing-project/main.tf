locals {
  project_id_parts        = split("/", var.project_resource_id)
  project_subscription_id = local.project_id_parts[2]
  project_resource_group  = local.project_id_parts[4]
  foundry_account_name    = local.project_id_parts[8]
  foundry_project_name    = local.project_id_parts[10]
  foundry_account_id      = join("/", slice(local.project_id_parts, 0, 9))
  normalized_project_id   = join("/", local.project_id_parts)
}

# The account and project are intentionally not Terraform resources. Only
# declared child resources are placed in state and owned by this module.
resource "azapi_resource" "model_deployment" {
  for_each = { for d in var.deployments : d.name => d }

  type      = "Microsoft.CognitiveServices/accounts/deployments@2025-06-01"
  name      = each.value.name
  parent_id = local.foundry_account_id

  body = {
    properties = {
      model = each.value.model
    }
    sku = each.value.sku
  }
}
