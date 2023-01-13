variable "location" {
  description = "The supported Azure location where the resource deployed"
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "tags" {
  description = "A list of tags used for deployed services."
  type        = map(string)
}

variable "sku" {
  description = "The pricing tier of this API Management service."
  type        = string
  default     = "Consumption"
}

variable "application_insights_name" {
  description = "Azure Application Insights Name."
  type        = string
}

variable "skuCount" {
  description = "The instance size of this API Management service. @allowed([ 0, 1, 2 ])"
  type        = string
  default     = "0"
}

variable "name" {
  type = string
}

variable "publisher_email" {
  description = "The email address of the owner of the service."
  type        = string
  default     = "noreply@microsoft.com"
}

variable "publisher_name" {
  description = "The name of the owner of the service"
  type = string
  default = "n/a"
}
