# Content Understanding client integration

The `azd ai cu` extension uses this Content Understanding client through its parent module's
`replace` directive. Before switching to a published package, verify the
following behavior with the extension's adapter and command tests.

## Required client behavior

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

## Extension responsibilities

The adapter separately enforces the CLI continuation path/API-version
contract using a shared link validator. Its pipeline policy also enforces the
extension's configured retry and attempt-timeout bounds against caller context
overrides.
Analyzer/analysis wire, polling and command integration tests use `RESTTransport`;
client resume-token support is not part of the CLI contract.
