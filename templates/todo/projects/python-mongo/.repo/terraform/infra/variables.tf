variable "location" {
  description = "The supported azure location where the resource deployed"
  type        = string
}

variable "name" {
  description = "The name of the azd evnironemnt to be deployed"
  type        = string
}

variable "principalId" {
  description = "The Id of the azd service principal to add to deployed keyvault access policies"
  type        = string
  default     = ""
}