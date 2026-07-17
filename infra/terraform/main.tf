# =============================================================================
# Foreman Infrastructure - Main Entry Point
# =============================================================================
# Deploys a cost-optimized EC2 instance for the Foreman orchestrator.
#
# Architecture:
#   - Single Graviton (ARM) EC2 instance (spot or on-demand)
#   - Docker for agent sandbox containers
#   - Neon free tier PostgreSQL (external, managed)
#   - Slack/Discord via Socket Mode (outbound WebSocket, no public endpoint)
#
# Estimated monthly cost:
#   t4g.small spot:  ~$4.38 compute + ~$1.60 storage = ~$5.98/mo
#   t4g.small on-demand: ~$12.26 compute + ~$1.60 storage = ~$13.86/mo
#   Neon free tier PostgreSQL: $0/mo
#
# Usage:
#   terraform init
#   terraform plan -var-file=terraform.tfvars
#   terraform apply -var-file=terraform.tfvars
# =============================================================================

# The actual resources are defined in:
#   - networking.tf   (VPC, subnet, Elastic IP)
#   - security.tf     (security group rules)
#   - compute.tf      (EC2 instance, spot request, AMI lookup)
#   - iam.tf          (IAM role, instance profile, policies)
#   - user-data.sh.tpl (first-boot script: Docker + Foreman)

# This file intentionally left as documentation.
# All resources are in their respective module files.
