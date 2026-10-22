# Release History

## 0.0.1 (Unreleased)

### Features Added

- Native Go extension scaffold with standard azd build, lint, and release integration.
- Context-free help, version, and command-tree metadata.
- Cancellable azd runtime context, structured errors, and safe debug diagnostics.
- Host-neutral CU client contracts, typed domain validation/errors, optional usage,
  and fake-client command tests for the initial GA and preview API baseline.
- Entra ID HTTP pipeline with validated endpoints/API versions, request correlation,
  read-only retries, bounded service errors, and offline authentication/redaction tests.
- Source-pinned local CU Go SDK runtime and extension-owned adapter covering all
  required analysis, analyzer, copy/authorization, and defaults operations while
  preserving raw envelopes, exact values, safe polling, and write retry safety.

### Bugs Fixed

- Guard all local outputs with verified directory ownership/permissions/ACLs,
  private staging, identity-checked cleanup, and atomic no-clobber publication.
  Reject unsafe shared or unsupported output locations without changing user permissions.
- Bound and redact diagnostic messages and metadata, omit customer-authored schema
  paths from errors, and suppress raw SDK log details. Preserve intentional result
  payloads and cancellation categories.
- Return safe validation errors for invalid command arguments/options instead of
  echoing supplied values; retain structured command errors and safe constraint locations.
- Prevent authentication challenges from replaying write submissions.
- Preserve configured request timeouts when disabling write retries, including
  submission and response-body deadlines.
- Preserve terminal service operation status and safe error codes so canceled
  operations retain CLI cancellation behavior and failures use operation-specific
  messages instead of reporting a failed HTTP 200 request.
- Restore analyzer create/copy final-resource reads and ready-state validation,
  retaining terminal usage when no analyzer result is embedded.
- Reject mismatched operation IDs and invalid CLI continuation paths/versions;
  preserve redacted nested diagnostic codes across SDK errors.
- Exercise analyzer/analysis wire and polling conformance through the production
  SDK adapter, including continuation headers, host casing, and cancellation
  before binary input access.
