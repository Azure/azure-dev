variable "name" {
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "apiName" {
  description = "Resouce name to uniquely dentify this API within the API Management service instance"
  type        = string
}

variable "apiDisplayName" {

  description = "The Display Name of the API"
  type        = string
  default     = length()
}

variable "apiPath" {
  description = "Relative URL uniquely identifying this API and all of its resource paths within the API Management service instance. It is appended to the API endpoint base URL specified during the service instance creation to form a public URL for this API."
  type        = string
}
