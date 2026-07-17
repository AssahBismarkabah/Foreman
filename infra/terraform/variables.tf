variable "aws_region" {
  description = "AWS region for all resources"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Deployment environment (dev/staging/prod)"
  type        = string
  default     = "dev"
}

variable "project_name" {
  description = "Project name used for resource naming"
  type        = string
  default     = "foreman"
}

# --- Compute ---

variable "instance_type" {
  description = "EC2 instance type (Graviton ARM recommended for cost)"
  type        = string
  default     = "t4g.small"
}

variable "use_spot_instance" {
  description = "Use spot instance to reduce cost (~60% savings)"
  type        = bool
  default     = true
}

variable "root_volume_size" {
  description = "Root EBS volume size in GB. Must be >= the AMI snapshot size (Amazon Linux 2023 requires 30GB)."
  type        = number
  default     = 30
}

variable "root_volume_type" {
  description = "Root EBS volume type"
  type        = string
  default     = "gp3"
}

# --- Networking ---

variable "vpc_id" {
  description = "VPC ID (defaults to default VPC if empty)"
  type        = string
  default     = ""
}

variable "subnet_id" {
  description = "Subnet ID (defaults to first public subnet in VPC if empty)"
  type        = string
  default     = ""
}

variable "ssh_cidr_blocks" {
  description = "CIDR blocks allowed to SSH into the instance"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "api_cidr_blocks" {
  description = "CIDR blocks allowed to access Foreman API (:8080). Slack/Discord use outbound WebSocket, so the API doesn't need to be public. Default is localhost only."
  type        = list(string)
  default     = ["127.0.0.1/32"]
}

# --- SSH ---

variable "ssh_public_key" {
  description = "SSH public key content for EC2 access. Set this in CI. Leave empty to use ssh_public_key_path."
  type        = string
  sensitive   = false
  default     = ""
}

variable "ssh_public_key_path" {
  description = "Path to SSH public key file (used only if ssh_public_key is empty)"
  type        = string
  default     = ""
}

# --- Foreman Config ---

variable "foreman_pg_dsn" {
  description = "PostgreSQL DSN (use Neon free tier connection string)"
  type        = string
  sensitive   = true
}

variable "foreman_signing_key" {
  description = "Base64-encoded RSA private key for JWT signing"
  type        = string
  sensitive   = true
  default     = ""
}

variable "slack_bot_token" {
  description = "Slack Bot Token (xoxb-*)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "slack_app_token" {
  description = "Slack App-Level Token (xapp-*)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "discord_bot_token" {
  description = "Discord Bot Token"
  type        = string
  sensitive   = true
  default     = ""
}
