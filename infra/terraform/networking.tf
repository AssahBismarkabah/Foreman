# --- VPC ---
# Use existing VPC or default VPC. For production, create a dedicated VPC.

data "aws_vpc" "selected" {
  id = var.vpc_id != "" ? var.vpc_id : data.aws_vpc.default.id
}

data "aws_vpc" "default" {
  default = true
}

# --- Subnet ---
# Use existing subnet or first public subnet from the VPC.

data "aws_subnet" "selected" {
  id = var.subnet_id != "" ? var.subnet_id : data.aws_subnet.default.id
}

data "aws_subnet" "default" {
  vpc_id            = data.aws_vpc.selected.id
  default_for_az    = true
  availability_zone = "${var.aws_region}a"
}

# --- Elastic IP ---
# Static IP for the Foreman instance. Free while attached to a running instance.
# NOTE: We use a separate aws_eip_association resource instead of the `instance`
# parameter on aws_eip, because the `instance` parameter does not work reliably
# with spot instances (spot_instance_id can return sir-xxx instead of i-xxx).

resource "aws_eip" "foreman" {
  domain = "vpc"

  tags = {
    Name = "${var.project_name}-${var.environment}-eip"
  }
}

resource "aws_eip_association" "foreman" {
  instance_id   = local.instance_id
  allocation_id = aws_eip.foreman.id
}
