terraform {
  required_version = ">= 1.1.7"
  backend "gcs" {
    bucket = "my-tf-state-bucket"
    prefix = "terraform/state"
  }
}
