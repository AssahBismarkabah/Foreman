# Mock LLM server for E2E tests.
#
# A minimal Go HTTP server that implements OpenAI /v1/chat/completions with
# canned SSE responses. No dependencies, ~5MB static binary on scratch.
#
# Built by TestMain as "foreman:e2e-mockllm".

FROM scratch
COPY mockllm /mockllm
EXPOSE 9999
ENTRYPOINT ["/mockllm"]
