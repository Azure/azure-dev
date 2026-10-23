# Content Understanding client for the azd extension

This Go module supplies the Content Understanding REST client used by
`azd ai cu`. It is an extension dependency, not a separately published package.
For installation, authentication, and command examples, see the
[extension README](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md).

## Client behavior

The client supports JSON and binary analysis, asynchronous and preview inline
analysis, analyzer management and copying, and service defaults. The default
API version is `2025-11-01`. Any valid `YYYY-MM-DD-preview` date is also accepted,
including `2026-06-01-preview`; the service determines version availability.
Inline analysis requires a preview version.

The extension supplies an azd-backed Microsoft Entra credential. It owns command
configuration, confirmations, file access, model discovery, and output formatting.
Client credential constructors do not change the extension's Entra-only
authentication contract.

- Responses preserve complete JSON definitions and terminal envelopes, including
  unknown fields, exact numbers, and usage.
- Binary input requires an `io.ReadSeekCloser`; the caller retains ownership and
  must keep it open until submission completes.
- Analyzer create and copy do not replace an existing analyzer. The extension
  owns destination checks and cross-resource authorization orchestration.
- Reads and polling use the configured retry policy and honor cancellation.
  Write submissions are not automatically retried or replayed after an
  authentication challenge.
- Service and operation failures preserve typed status and safe diagnostic codes
  without exposing raw service messages.

See [client integration requirements](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/cu-rest-client/AZD_PROVENANCE.md) for the behavior to verify
when changing the client dependency.

## Offline tests

Run from this module directory:

```bash
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...
```

Tests in [tests/unit/](tests/unit/) use the public client API with local HTTPS
servers and fake credentials. They do not require Azure sign-in or make live
service calls. Extension adapter and command tests remain under the parent
extension's [internal/](../internal/) directory.

The extension's CI runs tests and lint for both Go modules.

## Optional live tests

Live tests in [tests/live/](tests/live/) are guarded by the `live` build tag and
are not part of the ordinary test run. These client developer tests use Azure CLI
credentials, independently of the extension's azd authentication.

Requirements:

- Azure CLI sign-in with `az login`.
- `CONTENTUNDERSTANDING_ENDPOINT` for the test resource.
- `CONTENTUNDERSTANDING_RESOURCE_ID` and `CONTENTUNDERSTANDING_REGION` for
  authorization checks against the same resource.
- Existing supported model deployments and a `gpt-5.2` completion mapping.

Only run with authorization to use the designated resource:

```bash
go test -tags live -run TestLive -count=1 -timeout 30m -v ./tests/live/...
```

These tests incur service charges, analyze a public sample invoice, and create,
copy, and delete uniquely named test analyzers. They do not modify shared
resource defaults or establish cross-resource copy behavior. Passing client live
tests does not establish installed-host or release readiness for the extension.
