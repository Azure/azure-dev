# Agent Development Guide

A file for [guiding coding agents](https://agents.md/).

## Commands

- **Build:** `go build`
- **Test (Golang):** `go test ./... -short`
- **Test -- include functional end-to-end tests (Golang)**: `go test ./...`
- **Cspell check**: `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml`
- **Linter**: `golangci-lint run ./...`

## Directory Structure

- Functional tests: `test/`
- Docs: `docs/`
