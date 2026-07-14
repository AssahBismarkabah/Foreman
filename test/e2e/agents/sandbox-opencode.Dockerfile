# OpenCode sandbox image for E2E tests.
#
# This image is used as the sandbox container where opencode runs tasks.
# It uses the same foreman:e2e-opencode image (which has node + opencode)
# but strips the ENTRYPOINT so the Docker sandbox provider's Cmd
# (tail -f /dev/null) takes effect instead of running /foreman.
#
# Built by TestMain as "foreman:e2e-sandbox-opencode".
#
# IMPORTANT: Hardcode foreman:e2e-opencode here (not via ARG) because
# buildAgentImage always passes --build-arg BASE_IMAGE=foreman:e2e.
# Reuses all foreman:e2e-opencode layers -- build is ~instant.

FROM foreman:e2e-opencode
ENTRYPOINT []
