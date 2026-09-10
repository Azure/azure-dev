locals {
  project_id_parts        = split("/", var.project_resource_id)
  project_subscription_id = local.project_id_parts[2]
  project_resource_group  = local.project_id_parts[4]
  foundry_account_name    = local.project_id_parts[8]
  foundry_project_name    = local.project_id_parts[10]
  foundry_account_id      = join("/", slice(local.project_id_parts, 0, 9))
  normalized_project_id   = join("/", local.project_id_parts)
  project_endpoint_matches = regexall(
    "(?i)^https://([^.]+)\\.services\\.ai\\.azure\\.com/(?:api/)?projects/([^/?#]+)/?$",
    var.project_endpoint,
  )
  project_endpoint_account = length(local.project_endpoint_matches) == 1 ? local.project_endpoint_matches[0][0] : ""
  project_endpoint_project = length(local.project_endpoint_matches) == 1 ? local.project_endpoint_matches[0][1] : ""
}
