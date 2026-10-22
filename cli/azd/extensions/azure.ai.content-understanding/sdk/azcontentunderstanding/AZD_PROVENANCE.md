# Local Content Understanding Go SDK

This module is copied from
[`Azure/azure-sdk-for-go#27810`](https://github.com/Azure/azure-sdk-for-go/pull/27810)
at commit `8ff358e8d3410d9bc96b9ed79487a0cad0252fac`.

It is intentionally consumed through the parent extension module's relative
`replace` directive while the Content Understanding Go SDK is unpublished.
Ship and validate `azd ai cu` first. Track local runtime changes as candidate
back-ports to the SDK pull request after the extension ships.

The parent module selects its existing Azure SDK dependency versions. In
particular, it continues to use `azcore` v1.23.3; the copied module does not
require an `azcore` upgrade or downgrade.
The copied module's Go directive is aligned with the repository's Go 1.26.4 baseline.

## Local changes to disposition after azd ships

- Preserve analyzer create/copy terminal envelopes and operation-level usage.
- Retain succeeded envelopes without embedded analyzer results so the adapter
  can perform the final GET, preserving usage and enforcing ready-state completion.
- Preserve the complete defaults response JSON.
- Mark malformed successful responses with `ErrInvalidResponse`.
- Preserve unsuccessful terminal status and safe service codes in `OperationError`,
  wrapping `azcore.ResponseError` while leaving CLI error classification to the adapter.
- Retain sanitized nested operation diagnostic codes without service messages or targets.
- Bound and sanitize service and polling errors before creating
  `azcore.ResponseError` values.
- Use a same-resource polling handler that retains terminal envelopes without
  logging service error bodies.
- Bind returned operation IDs to the initial operation location and reject mismatches.
  Resolve relative locations against the submission URL and canonicalize host casing.
- Preserve `Accept: application/json` on continuation requests and accept
  case-insensitive host matches on the same configured origin.
- Stop canceled analysis before seeking or reading caller-owned binary input.
- Disable automatic retries and authentication-challenge replay for write
  submissions while preserving configured request timeouts and retaining
  bounded retries for reads and polling.

The adapter separately enforces the existing CLI continuation path/API-version
contract using a shared link validator. Its pipeline policy also enforces the
extension's configured retry and attempt-timeout bounds against caller context
overrides. These are adapter policies, not additional patches to this SDK copy.
Analyzer/analysis wire, polling and command integration tests use `SDKTransport`;
the obsolete parallel REST implementation and its helper-only tests have been
removed. This does not claim generic SDK resume-token parity.
