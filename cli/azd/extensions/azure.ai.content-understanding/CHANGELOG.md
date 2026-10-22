# Release History

## 0.0.1 (Unreleased)

### Features Added

- Source-pinned 797-case parity ledger with explicit pending/evidenced/approved
  states, structural completeness checks, and a separate fail-closed approval gate.
- Sanitized defaults/analyzer-list recordings exercised through the production
  Go SDK adapter and command boundary, with strict offline matching, full-result
  assertions, provenance, hygiene, and unexpected-request/network-denial tests.
- Three recorded GA analysis scenarios with all ordered polling interactions,
  complete streamed/saved results, and virtual-time SDK execution. Distinguish
  synthetic binary/URL requests and polling-failure contracts from recorded
  service responses; retain public-sample provenance and binary evidence limits.
- Recorded analyzer create/show/delete lifecycle replay for both public schema
  variants, complete definitions, ordered polling, explicit resource reads and
  empty-204 deletion. Declare scrubbed JSON expectations/no-replacement queries,
  embedded-result versus synthetic final-GET fallback, and source scenario-label limits.
- Native Go extension scaffold with standard azd build, lint, and release integration.
- Recorded analyzer-test field report with all 45 interactions and 43 Running
  polls, compared in full against identical offline Python Azure/azd reports.
  Preserve all 56 fields, exact statistics, nullable confidence and disclaimer;
  separately label synthetic warning/usage and saved-failure report contracts.
- Eight extraction-template/classifier/route recordings retain all 22
  interactions, full service definitions, offline Python template-oracle
  comparisons and strict/spec validation. Preserve ready-result polling
  shortcuts, explicit final reads and reverse dependency deletion; label
  synthetic pending-start and scrubbed-request evidence separately.
- Recorded doctor and sample-schema suggestion complete the 19-cassette import:
  full independent frontend outputs, declared auth/context/model differences,
  all 51 suggestion interactions/49 Running polls and all 20 suggested fields.
  Explicitly distinguish JSON-to-binary request adaptation, synthetic missing
  defaults/warning/fallback contracts and unproven live/host/release evidence.
- All 11 source hygiene scenarios have exact source-hashed synthetic controls
  and native rejection/positive-control evidence, plus isolated sensitive-header,
  query-key and CU-host regressions. Native rejects unsafe imports, including
  REDACTED credential markers, rather than implementing a recording scrubber.
  Keep this stricter replacement unapproved and UTF-8 artifact normalization
  distinct from byte-return API equivalence.
- Map all 19 per-cassette source hygiene cases to exact native fixture subtests
  and isolated private-marker/secret-assignment controls. Cover quoted and
  whitespace-separated eight-character values and Python Unicode matching
  behavior; distinguish normalized JSON checks from source YAML and keep the
  stricter REDACTED policy unapproved.
- Map four source recording-helper cases to full source-executed controls and
  exact import-set checks. Pin three code hashes and expected cassette names;
  reject missing/duplicate/extra files and non-placeholder request origins.
  Keep scrubber/REDACTED/UTF-8 artifact differences unapproved. Totals:
  61 cases evidenced at that checkpoint.
- Map 29 source manifest cases to exact per-scenario structural checks, source
  counts and cassette ownership. Pin a byte-identical source YAML snapshot and
  reject identity/order/association/reference drift independently of review
  metadata. Live-only shape checks do not execute live workflows or approve
  behavior. Totals at that checkpoint: 90 cases evidenced, 707 pending.
- Map three source normalized-adapter cases to complete source-executed
  observations/backend calls and production-SDK-delegating native checks.
  Declare synthetic ready-status additions for source fake models and exact
  conflict-error differences; retain original source objects and reject
  unadapted models without weakening production validation. Compare full
  default/JSON output with separate quiet-output assertions across 24
  invocations. This is synthetic HTTP evidence, not another cassette or live
  validation. Totals: 93 cases evidenced, 704 pending; no new approvals or
  scenario-state changes.
- Context-free help, version, and command-tree metadata.
- Map four source generated-project cases to complete ten-file tree/written-set
  checks and actual Bash hook delegation for both Python origins. Preserve all
  source hashes/choices/arguments and explicit native README/config-key-order
  differences; compare full config JSON separately. Check owner-execute mode,
  not just script text. PowerShell cases stay pending without `pwsh`; no claim
  of installed-host/live/Windows execution. Totals: 97 cases evidenced, 700
  pending; prior assets, scenario states and all approvals remain unchanged.
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
