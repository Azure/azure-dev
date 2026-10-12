# Reporting and Error Handling

## Reporting

Treat the `go tool mage preflight` exit status and output as authoritative. Do not reproduce the
full check-by-check transcript. Report whether preflight passed, any fixes applied, and any
remaining failed or skipped checks. Never describe a nonzero exit as partial success.

## Error Handling

- **golangci-lint not installed** → offer: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4`
- **cspell not installed** → offer to install: `npm install -g cspell@8.13.1`
- **gh not installed** → offer installation guidance from https://cli.github.com/
- **bash/sh not found (Windows)** → suggest Git for Windows: https://git-scm.com/downloads/win
- **Go version mismatch** → preflight sets `GOTOOLCHAIN` automatically; report version conflict if persists
- **Preflight timeout** → unit tests can take 10+ min; use at least 15-min timeout
- **Cannot determine repo root** → ensure cwd is within the `azure-dev` repository
