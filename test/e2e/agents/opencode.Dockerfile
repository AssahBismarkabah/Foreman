# OpenCode agent test image.
#
# Extends the pre-built foreman:e2e image by adding Node.js and the opencode
# CLI so the opencode adapter passes Verify() and can execute real tasks.
#
# Built by TestMain as "foreman:e2e-opencode" using:
#   docker build -t foreman:e2e-opencode \
#     -f test/e2e/agents/opencode.Dockerfile \
#     test/e2e/agents
#
# The --build-arg BASE_IMAGE=foreman:e2e is injected by TestMain.
ARG BASE_IMAGE=foreman:e2e

# First stage: reference the pre-built image so we can COPY from it by name.
FROM ${BASE_IMAGE} AS foreman-base

# Second stage: Node.js Alpine with foreman + opencode.
FROM node:23-alpine

# Copy the statically compiled foreman binary from the pre-built image stage.
COPY --from=foreman-base /foreman /foreman

# Install opencode CLI globally from npm.
# The npm package name is "opencode-ai" (not "opencode" -- that returns 404).
# Pin the version to match the opencode binary CI installs for the host
# (see .github/workflows/ci.yml). Using @latest is non-reproducible and
# has broken the E2E mock LLM contract when a newer version ships.
RUN npm install -g opencode-ai@1.17.18

# Verify the binary is findable (catches npm install issues early).
RUN opencode --version

ENTRYPOINT ["/foreman"]
