# Foreman Infrastructure

Cost-optimized AWS deployment for the Foreman orchestrator.

## Architecture

```
Slack/Discord ──WebSocket──▶ Foreman ──Docker socket──▶ Sandbox Containers
                                │
                                ├──▶ Neon PostgreSQL (free tier)
                                └──▶ GitHub (code access)
```

- **Compute:** Single EC2 Graviton (ARM) instance -- t4g.small spot (~$6/mo)
- **Database:** Neon free tier PostgreSQL ($0/mo)
- **Sandbox:** Docker containers via host socket mount
- **Communication:** Slack Socket Mode / Discord Gateway (outbound WebSocket only)

## Estimated Monthly Cost

| Component | Option | Cost |
|---|---|---|
| EC2 t4g.small spot | 2 vCPU, 2 GB RAM | ~$4.38/mo |
| EBS gp3 | 30 GB | ~$2.40/mo |
| Neon PostgreSQL | Free tier (100 CU-hours, 3 GB) | $0/mo |
| **Total** | | **~$5.98/mo** |

## Directory Structure

```
infra/
├── diagrams/
│   └── deployment.puml        # PlantUML deployment diagram
├── terraform/
│   ├── providers.tf           # Terraform + AWS provider config
│   ├── variables.tf           # All input variables
│   ├── outputs.tf             # Output values (IP, SSH command, cost)
│   ├── networking.tf          # VPC, subnet, Elastic IP
│   ├── security.tf            # Security group rules
│   ├── compute.tf             # EC2 instance + spot request
│   ├── iam.tf                 # IAM role + instance profile
│   ├── main.tf                # Documentation entry point
│   ├── user-data.sh.tpl       # First-boot script (Docker + Foreman)
│   └── terraform.tfvars.example
├── ansible/
│   ├── ansible.cfg            # Ansible configuration
│   ├── inventory.yml          # Host inventory
│   ├── playbook.yml           # Main playbook
│   ├── vars/
│   │   └── production.yml     # Production variable overrides
│   └── roles/
│       ├── docker/
│       │   └── tasks/main.yml # Docker installation
│       └── foreman/
│           ├── tasks/main.yml # Foreman deployment
│           ├── templates/
│           │   ├── foreman.yaml.j2
│           │   └── foreman.service.j2
│           ├── handlers/main.yml
│           └── vars/main.yml
└── README.md
```

## Quick Start

### Prerequisites

- AWS account with programmatic access
- [Neon](https://neon.tech) account (free) for PostgreSQL
- Terraform >= 1.6
- Ansible >= 2.15 (optional, for config management)

### 1. Set up Neon Database

1. Sign up at https://neon.tech
2. Create a project (select region `US East (N. Virginia)` for lowest latency)
3. Copy the connection string from the dashboard

### 2. Configure Terraform

```bash
cd terraform

# Copy and edit the configuration
cp terraform.tfvars.example terraform.tfvars

# Fill in your values:
#   - foreman_pg_dsn: your Neon connection string
#   - ssh_public_key_path: path to your SSH public key (or set ssh_public_key directly)
#   - slack_bot_token / slack_app_token (if using Slack)
```

### 3. Deploy

```bash
# Initialize Terraform
terraform init

# Preview changes
terraform plan -var-file=terraform.tfvars

# Apply (creates EC2 instance + installs Docker + Foreman)
terraform apply -var-file=terraform.tfvars

# Get the instance IP
terraform output instance_public_ip
```

### 4. Verify

```bash
# SSH into the instance
terraform output ssh_command

# Check Foreman status
ssh ec2-user@<ip> sudo systemctl status foreman

# Check health endpoint (from the instance itself, or set api_cidr_blocks to your IP)
ssh ec2-user@$(terraform output -raw instance_public_ip) \
  "curl -s http://localhost:8080/healthz"
```

## CI/CD Pipeline

The GitHub Actions workflow in `.github/workflows/infra.yml` automates:

1. **Validate** -- Terraform formatting and syntax check (auto on push/PR)
2. **Plan** -- Terraform plan, dry run (auto on push/PR)
3. **Apply** -- Provision infrastructure (**manual only** via `workflow_dispatch`)
4. **Configure** -- Ansible playbook (Docker + Foreman setup)
5. **Verify** -- Health check against Foreman API

### Required GitHub Secrets

Add these in your repo: **Settings > Secrets and variables > Actions > New repository secret**

| Secret | Required | Description |
|---|---|---|
| `AWS_ACCESS_KEY_ID` | Yes | AWS IAM access key |
| `AWS_SECRET_ACCESS_KEY` | Yes | AWS IAM secret key |
| `FOREMAN_PG_DSN` | Yes | Neon PostgreSQL connection string |
| `SSH_PRIVATE_KEY` | Yes | SSH private key for EC2 access (public key is derived automatically in CI) |
| `SLACK_BOT_TOKEN` | No | Slack bot token |
| `SLACK_APP_TOKEN` | No | Slack app-level token |
| `DISCORD_BOT_TOKEN` | No | Discord bot token |
| `FOREMAN_SIGNING_KEY` | No | Base64-encoded RSA key |
| `GHCR_PAT` | No | GitHub PAT with `read:packages` scope (for private GHCR packages on first boot) |

## Cost Optimization Notes

- **Spot instances** save ~60% vs on-demand. If interrupted, the instance stops and
  restarts when capacity is available. Foreman's state is in Neon (persistent).
- **Graviton (ARM)** is 20% cheaper than x86 for the same performance.
- **Neon free tier** eliminates RDS costs ($0 vs ~$14/mo for RDS db.t4g.micro).
- **Slack Socket Mode** is outbound-only -- no load balancer or public DNS needed.
- **gp3 volumes** are 20% cheaper than gp2 with better baseline performance.

## Upgrading Foreman

```bash
# SSH into the instance
ssh ec2-user@<ip>

# Pull the latest image and restart
sudo systemctl restart foreman

# Or via Ansible (from your CI/CD machine):
# ansible-playbook -i inventory.yml playbook.yml --tags foreman
```
