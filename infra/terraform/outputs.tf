output "instance_id" {
  description = "EC2 instance ID"
  value       = local.instance.id
}

output "instance_public_ip" {
  description = "Public IP address of the Foreman instance"
  value       = local.instance.public_ip
}

output "instance_private_ip" {
  description = "Private IP address of the Foreman instance"
  value       = local.instance.private_ip
}

output "instance_public_dns" {
  description = "Public DNS name of the Foreman instance"
  value       = local.instance.public_dns
}

output "ssh_command" {
  description = "SSH command to connect to the instance (adjust key path if needed)"
  value       = "ssh -i ~/.ssh/id_ed25519 ec2-user@${local.instance.public_ip}"
}

output "instance_type" {
  description = "EC2 instance type"
  value       = local.instance.instance_type
}

output "pricing_model" {
  description = "Pricing model (spot or on-demand)"
  value       = var.use_spot_instance ? "spot" : "on-demand"
}

output "estimated_monthly_cost" {
  description = "Estimated monthly cost (compute + storage only)"
  value = var.use_spot_instance ? (
    format("~$%.2f/mo (spot compute: ~$%.2f + storage: ~$%.2f)",
      local.spot_monthly + local.storage_monthly,
      local.spot_monthly,
    local.storage_monthly)
    ) : (
    format("~$%.2f/mo (on-demand compute: ~$%.2f + storage: ~$%.2f)",
      local.on_demand_monthly + local.storage_monthly,
      local.on_demand_monthly,
    local.storage_monthly)
  )
}
