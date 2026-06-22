# Input variables for the Foundry Terraform module. These mirror the params
# of the Bicep template (internal/synthesis/templates/main.bicep). azd-core
# substitutes the ${...} placeholders in main.tfvars.json from the azd
# environment at provision time; deployments and include_acr are written by
# the eject step from azure.yaml.

variable "subscription_id" {
  description = "Azure subscription id. Supplied by azd from AZURE_SUBSCRIPTION_ID."
  type        = string
}

variable "location" {
  description = "Azure region for all resources."
  type        = string
}

variable "resource_group_name" {
  description = "Name of the resource group to create and deploy resources into."
  type        = string
}

variable "environment_name" {
  description = "azd environment name. Used to tag resources (azd-env-name)."
  type        = string
}

variable "tags" {
  description = "Tags applied to all resources."
  type        = map(string)
  default     = {}
}

variable "resource_token_salt" {
  description = "Optional salt to vary resource names across re-provisions."
  type        = string
  default     = ""
}

variable "foundry_project_name" {
  description = <<-EOT
    Foundry project name (3-32 alphanumeric/hyphen chars). When empty, it is
    derived from environment_name (see local.foundry_project_name in main.tf),
    mirroring the Bicep provider's default-to-env-name behavior.
  EOT
  type        = string
  default     = ""

  validation {
    condition     = var.foundry_project_name == "" || can(regex("^[a-zA-Z0-9-]{3,32}$", var.foundry_project_name))
    error_message = "foundry_project_name must be empty or 3-32 alphanumeric/hyphen characters."
  }
}

variable "deployments" {
  description = "Model deployments to provision on the Foundry account."
  type = list(object({
    name = string
    model = object({
      name    = string
      format  = string
      version = string
    })
    sku = object({
      name     = string
      capacity = number
    })
  }))
  default = []
}

variable "include_acr" {
  description = "Include an Azure Container Registry. Set true when any agent uses docker."
  type        = bool
  default     = false
}

variable "principal_id" {
  description = <<-EOT
    Object id of the developer running azd. When set, grants Cognitive Services
    User on the project. Empty disables the role assignment so headless / CI
    runs do not fail.
  EOT
  type        = string
  default     = ""
}

variable "principal_type" {
  description = "Principal type used in the developer role assignment."
  type        = string
  default     = "User"
}
