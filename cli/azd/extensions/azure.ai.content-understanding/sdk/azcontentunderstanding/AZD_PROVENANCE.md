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

## Local changes to disposition after azd ships

- Preserve analyzer create/copy terminal envelopes and operation-level usage.
- Preserve the complete defaults response JSON.
- Mark malformed successful responses with `ErrInvalidResponse`.
- Bound and sanitize service and polling errors before creating
  `azcore.ResponseError` values.
- Use a same-resource polling handler that retains terminal envelopes without
  logging service error bodies.
- Disable automatic retries for write submissions while retaining bounded
  retries for reads and polling.
