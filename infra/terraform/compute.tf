# --- SSH Key Pair ---

resource "aws_key_pair" "foreman" {
  key_name   = "${var.project_name}-${var.environment}-key"
  public_key = var.ssh_public_key != "" ? var.ssh_public_key : file(var.ssh_public_key_path)
}

# --- EC2 Instance ---
# Graviton (ARM) for best price/performance. Spot for ~60% savings.
# Uses instance_market_options for spot pricing (recommended over
# aws_spot_instance_request, whose spot_instance_id attribute is documented
# as "informational only, not for resource dependencies").

locals {
  # Pricing estimates (us-east-1, verified July 2026)
  instance_pricing = {
    "t4g.nano"   = { on_demand = 0.0042, spot = 0.0020 }
    "t4g.micro"  = { on_demand = 0.0084, spot = 0.0030 }
    "t4g.small"  = { on_demand = 0.0168, spot = 0.0060 }
    "t4g.medium" = { on_demand = 0.0336, spot = 0.0120 }
  }

  pricing           = lookup(local.instance_pricing, var.instance_type, { on_demand = 0.0168, spot = 0.0060 })
  hourly_rate       = var.use_spot_instance ? local.pricing.spot : local.pricing.on_demand
  spot_monthly      = local.hourly_rate * 730
  on_demand_monthly = local.pricing.on_demand * 730

  # EBS gp3: $0.08/GB-month
  storage_monthly = var.root_volume_size * 0.08
}

locals {
  # Single instance resource -- always aws_instance (spot via instance_market_options)
  instance    = aws_instance.foreman[0]
  instance_id = aws_instance.foreman[0].id
}

# --- EC2 Instance (on-demand or spot via instance_market_options) ---

resource "aws_instance" "foreman" {
  count = 1

  ami                    = data.aws_ami.amazon_linux_2023.id
  instance_type          = var.instance_type
  subnet_id              = data.aws_subnet.selected.id
  vpc_security_group_ids = [aws_security_group.foreman.id]
  key_name               = aws_key_pair.foreman.key_name
  iam_instance_profile   = aws_iam_instance_profile.foreman.name

  # Ensure security group ingress rules exist before instance launches
  depends_on = [
    aws_vpc_security_group_ingress_rule.ssh,
    aws_vpc_security_group_ingress_rule.api,
  ]

  dynamic "instance_market_options" {
    for_each = var.use_spot_instance ? [1] : []
    content {
      market_type = "spot"
      spot_options {
        instance_interruption_behavior = "stop"
        spot_instance_type             = "persistent"
      }
    }
  }

  root_block_device {
    volume_type = var.root_volume_type
    volume_size = var.root_volume_size
    encrypted   = true
    tags = {
      Name = "${var.project_name}-${var.environment}-root"
    }
  }

  user_data_base64 = base64encode(templatefile("${path.module}/user-data.sh.tpl", {
    foreman_version     = "latest"
    foreman_pg_dsn      = var.foreman_pg_dsn
    foreman_signing_key = var.foreman_signing_key
    slack_bot_token     = var.slack_bot_token
    slack_app_token     = var.slack_app_token
    discord_bot_token   = var.discord_bot_token
    ghcr_token          = var.ghcr_token
    ghcr_username       = "github-actions"
  }))

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "enabled"
  }

  monitoring = true

  tags = {
    Name = "${var.project_name}-${var.environment}"
  }
}

# --- AMI Lookup ---

data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-arm64"]
  }

  filter {
    name   = "architecture"
    values = ["arm64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "state"
    values = ["available"]
  }
}
