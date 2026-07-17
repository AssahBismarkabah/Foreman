# --- Security Group ---
# Minimal access: SSH from admin IPs, API from configured CIDRs.

resource "aws_security_group" "foreman" {
  name        = "${var.project_name}-${var.environment}-sg"
  description = "Foreman orchestrator security group"
  vpc_id      = data.aws_vpc.selected.id

  tags = {
    Name = "${var.project_name}-${var.environment}-sg"
  }
}

# SSH access (restrict to your IP in production)
resource "aws_vpc_security_group_ingress_rule" "ssh" {
  for_each = toset(var.ssh_cidr_blocks)

  security_group_id = aws_security_group.foreman.id
  description       = "SSH from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
}

# Foreman API access (port 8080)
resource "aws_vpc_security_group_ingress_rule" "api" {
  for_each = toset(var.api_cidr_blocks)

  security_group_id = aws_security_group.foreman.id
  description       = "Foreman API from ${each.value}"
  cidr_ipv4         = each.value
  from_port         = 8080
  to_port           = 8080
  ip_protocol       = "tcp"
}

# Allow all outbound traffic (Foreman connects outbound via WebSocket)
resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.foreman.id
  description       = "All outbound traffic"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}
