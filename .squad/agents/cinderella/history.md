# Cinderella — History

## Project Context
Azure Developer CLI (azd) — Go 1.26 CLI for Azure app development and deployment.
Stack: Go, gRPC/protobuf, Cobra, Azure SDK, Bicep/Terraform, Docker.
Owner: Shayne Boyer.

## Learnings
- Deploy timeout is now a global `azd deploy --timeout` concern only; `azure.yaml` service config and schema should not carry per-service deploy timeout settings.
