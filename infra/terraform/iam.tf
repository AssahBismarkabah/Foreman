# --- IAM Role for EC2 ---
# Enables SSM Session Manager (no SSH key needed) and CloudWatch logs.

data "aws_iam_policy_document" "ec2_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "foreman" {
  name               = "${var.project_name}-${var.environment}-ec2-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json

  tags = {
    Name = "${var.project_name}-${var.environment}-ec2-role"
  }
}

# SSM Managed Instance Core (for Session Manager access)
resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.foreman.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# CloudWatch agent policy (for log shipping)
resource "aws_iam_role_policy_attachment" "cloudwatch_agent" {
  role       = aws_iam_role.foreman.name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
}

# Instance profile
resource "aws_iam_instance_profile" "foreman" {
  name = "${var.project_name}-${var.environment}-instance-profile"
  role = aws_iam_role.foreman.name
}
