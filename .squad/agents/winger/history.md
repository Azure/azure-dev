# Winger — History

## Project Context
Azure Developer CLI (azd) — Go 1.26 CLI for Azure app development and deployment.
Stack: Go, gRPC/protobuf, Cobra, Azure SDK, Bicep/Terraform, Docker.
Owner: Shayne Boyer.

## Learnings

- Deploy timeouts in `cli/azd/internal/cmd/deploy.go` should wrap `context.DeadlineExceeded` with a service-specific, user-friendly message instead of returning the raw timeout error.
- The `deployTimeout` schema in `schemas/v1.0/azure.yaml.json` should enforce `"minimum": 1` so invalid zero or negative values are rejected at validation time.
