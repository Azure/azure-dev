variable "location" {
  description = "The supported Azure location where the resource deployed"
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
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

variable "tags" {
  description = "A list of tags used for deployed services."
  type        = map(string)
}

variable "resource_token" {
  description = "A suffix string to centrally mitigate resource name collisions."
  type        = string
}

variable "python_version" {
  description = "the application stack python version to set for the app service."
  type        = string
  default     = "3.8"
}

variable "sku" {
  description = "The pricing tier of this API Management service."
  type        = string
  default     = "Consumption"
}

variable "applicationInsightsName" {
  description = "Azure Application Insights Name."
  type        = string
}

variable "skuCount" {
  description = "The instance size of this API Management service. @allowed([ 0, 1, 2 ])"
  type        = list(0,1,2) 
  default     = "0"
}

variable "name" {
  type = string
}

variable "publisherEmail" {
  description = "The email address of the owner of the service."
  type        = string
  default     = "noreply@microsoft.com"
}

variable "publisherName" {
  description = "The name of the owner of the service"
  type = string
  default = "n/a"
}
