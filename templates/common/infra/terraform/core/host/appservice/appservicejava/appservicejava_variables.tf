variable "location" {
  description = "The supported Azure location where the resource deployed"
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "appservice_plan_id" {
  description = "The id of the appservice plan to use."
  type        = string
}

variable "service_name" {
  description = "A name to reflect the type of the app service e.g: web, api."
  type        = string
}

variable "app_settings" {
  description = "A list of app settings pairs to be assigned to the app service"
  type        = map(string)
}

variable "identity" {
  description = "A list of application identity"
  type        = list(any)
  default     = []
}

variable "app_command_line" {
  description = "The cmd line to configure the app to run."
  type        = string
}

variable "tags" {
  description = "A list of tags used for deployed services."
  type        = map(string)
}

variable "resource_token" {
  description = "A suffix string to centrally mitigate resource name collisions."
  type        = string
}

variable "java_version" {
  description = "the application stack java version to set for the app service."
  type        = string
  default     = "17"
}
