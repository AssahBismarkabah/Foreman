terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # State stored locally by default.
  # For teams, migrate to S3 + DynamoDB:
  #   backend "s3" {
  #     bucket         = "foreman-terraform-state"
  #     key            = "infra/terraform.tfstate"
  #     region         = "us-east-1"
  #     dynamodb_table = "foreman-terraform-locks"
  #     encrypt        = true
  #   }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "foreman"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}
