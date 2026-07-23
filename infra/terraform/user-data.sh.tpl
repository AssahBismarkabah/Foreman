#!/bin/bash
# Foreman EC2 User Data Script
# Runs on first boot to install Docker and configure Foreman.
set -euo pipefail

exec > >(tee /var/log/foreman-bootstrap.log | logger -t foreman-bootstrap) 2>&1

echo "=== Foreman Bootstrap Starting ==="

# --- System Updates ---
dnf update -y
dnf install -y docker jq git

# --- Disable firewalld ---
# The Foreman container uses --network host, so the host firewall directly
# controls access to container ports. firewalld blocks all non-SSH ports by
# default on Amazon Linux 2023, which would prevent external health checks
# from reaching port 8080 even though the security group allows it.
systemctl disable --now firewalld 2>/dev/null || true

# --- Docker Setup ---
systemctl enable --now docker
usermod -aG docker ec2-user

# Enable Docker to start on boot
systemctl enable docker

# --- Create Foreman Directory ---
mkdir -p /opt/foreman/{bin,config,data}
cd /opt/foreman

# --- GHCR Authentication ---
# If a GHCR token is provided, log in to pull private images.
%{ if ghcr_token != "" ~}
echo "Logging in to GHCR..."
echo "${ghcr_token}" | docker login ghcr.io -u "${ghcr_username}" --password-stdin
%{ endif ~}

# --- Pull Foreman Binary ---
# Strategy: try GHCR image first, fall back to building from source.
# For production, use the pre-built Docker image from the release workflow.

FOREMAN_VERSION="${foreman_version}"

if [ "$FOREMAN_VERSION" = "latest" ]; then
    # Pull the latest release from GHCR
    docker pull ghcr.io/assahbismarkabah/foreman:latest 2>/dev/null || {
        echo "GHCR pull failed, building from source..."
        git clone https://github.com/foreman/foreman.git /tmp/foreman-src
        cd /tmp/foreman-src
        docker build -t foreman:local .
        cd /opt/foreman
    }
fi

# --- Create Foreman Config ---
cat > /opt/foreman/config/foreman.yaml << 'FOREMAN_YAML'
# Foreman Configuration - Managed by Terraform
# Do not edit manually - changes will be overwritten on next deploy.

subsystems:
  eventbus:
    kind: memory

  statestore:
    kind: postgres
    dsn: "${foreman_pg_dsn}"
    max_connections: 25
    min_connections: 5

  sandbox:
    kind: docker
    image: ubuntu:22.04

  coordinator:
    max_concurrent: 5
    default_timeout: 5m
    heartbeat_interval: 5s
    heartbeat_timeout: 15s

  agents:
    - name: exec
      kind: exec
      cmd: sh
      cwd: /workspace
      heartbeat_timeout: 60s
    - name: opencode
      kind: opencode
      cmd: npx opencode
      cwd: /tmp/opencode-workspace
      heartbeat_interval: 30s
      heartbeat_timeout: 90s

  plugins:
%{ if slack_bot_token != "" && slack_app_token != "" ~}
    slack:
      bot_token: "${slack_bot_token}"
      app_token: "${slack_app_token}"
%{ endif ~}
%{ if discord_bot_token != "" ~}
    discord:
      bot_token: "${discord_bot_token}"
%{ endif ~}

  identity:
    api:
      listen_addr: ":8080"
      public_url: "http://localhost:8080"
    signing_key:
      source: env
      env_var_name: FOREMAN_SIGNING_KEY
      key_id: foreman-1
FOREMAN_YAML

# --- Create Environment File ---
cat > /opt/foreman/config/env << ENVFILE
%{ if foreman_signing_key != "" ~}FOREMAN_SIGNING_KEY=${foreman_signing_key}
%{ endif ~}DOCKER_HOST=unix:///var/run/docker.sock
ENVFILE
chmod 600 /opt/foreman/config/env

# --- Create Systemd Service ---
cat > /etc/systemd/system/foreman.service << 'SERVICE'
[Unit]
Description=Foreman Orchestrator
Documentation=https://github.com/foreman/foreman
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/foreman

EnvironmentFile=/opt/foreman/config/env

ExecStartPre=/usr/bin/docker pull ghcr.io/assahbismarkabah/foreman:latest
ExecStart=/usr/bin/docker run \
    --rm \
    --name foreman \
    --user root \
    --network host \
    -v /opt/foreman/config/foreman.yaml:/etc/foreman/foreman.yaml:ro \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e FOREMAN_SIGNING_KEY \
    -e DOCKER_HOST \
    ghcr.io/assahbismarkabah/foreman:latest \
    --config /etc/foreman/foreman.yaml

ExecStop=/usr/bin/docker stop foreman
ExecStopPost=/usr/bin/docker rm -f foreman 2>/dev/null || true

Restart=always
RestartSec=10

# Security
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/foreman/data

[Install]
WantedBy=multi-user.target
SERVICE

# --- Reload systemd and start Foreman ---
systemctl daemon-reload
systemctl enable foreman
systemctl start foreman

echo "=== Foreman Bootstrap Complete ==="
echo "Instance public IP: $(curl -s http://169.254.169.254/latest/meta-data/public-ipv4)"
echo "Check status: systemctl status foreman"
echo "View logs: journalctl -u foreman -f"
