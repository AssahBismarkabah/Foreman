# Foreman Infrastructure

This directory is the staging area for Foreman's infrastructure deployment code.
The content here defines the cost-optimized AWS deployment for the Foreman
orchestrator.

Components currently staged here:

- [`terraform/`](./terraform) - Terraform configuration for AWS infrastructure
  (VPC, subnet, Elastic IP, security groups, EC2 instance, IAM, first-boot user data).
- [`ansible/`](./ansible) - Ansible playbooks and roles for configuring the EC2
  instance (Docker installation, Foreman deployment, systemd service, health checks).
- [`diagrams/`](./diagrams) - PlantUML architecture diagrams.
- [`.github/workflows/infra.yml`](../.github/workflows/infra.yml) - GitHub Actions
  CI/CD pipeline that validates, plans, applies, configures, and verifies the deployment.

The code in the `infra/` directory is authoritative, i.e. the only copy of the
infrastructure deployment code. You can directly modify such code.

## Using infrastructure from Foreman code

Terraform provisioning runs through the GitHub Actions workflow defined in
`.github/workflows/infra.yml`. The workflow validates, plans, applies, and then
runs Ansible to configure the instance. Apply and destroy are manual-only via
`workflow_dispatch`.

To run Ansible locally against an existing instance:

```bash
cd infra/ansible
export FOREMAN_HOST=$(terraform -chdir=../terraform output -raw instance_public_ip)
export FOREMAN_PG_DSN="your-neon-dsn"
ansible-playbook -i inventory.yml playbook.yml
```

## Creating a new infrastructure component in staging

### Adding the component in Foreman:

1. Determine whether the component belongs in Terraform (infrastructure provisioning)
   or Ansible (configuration management).
2. Add the new files under the appropriate subdirectory.
3. Update this README to document the new component.
4. If the component requires secrets, add them to the GitHub Actions workflow
   in `.github/workflows/infra.yml` and document them in the workflow's secret list.
5. Run `terraform fmt` and `terraform validate` for Terraform changes, or
   `ansible-playbook --check` for Ansible changes, before submitting.
