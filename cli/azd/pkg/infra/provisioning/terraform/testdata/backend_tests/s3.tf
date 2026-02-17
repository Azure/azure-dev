terraform {
  required_version = ">= 1.1.7"
  backend "s3" {
    bucket = "my-tf-state-bucket"
    key    = "terraform.tfstate"
    region = "us-east-1"
  }
}
