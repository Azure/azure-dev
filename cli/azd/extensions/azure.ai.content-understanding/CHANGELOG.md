# Release History

## 0.0.1 (Unreleased)

### Features Added

- Simplify remaining command tests into native Go contracts: remove Python
  type/node assertions, cross-frontend observation wrappers, duplicate
  generated-project executions and review-state validation. Retain complete
  native request/output/error assertions, asset provenance and all 19 strict
  SDK playback recordings. No production behavior changes.
- Align the CLI-only extension with native azd conventions used by Foundry
  agents/projects. Migrate copy, schema, analyzer-test and generated-project
  integration tests to the production SDK adapter, remove the unused parallel
  REST implementation, and exercise retry/authentication/error regressions
  through real SDK operations. Enforce extension retry/attempt-timeout bounds
  against caller context overrides without replaying writes. Retain strict
  recorded playback and all reused-asset provenance.
- Native Go behavior/risk testing with Python cases used only as references.
  Remove the exhaustive source-node ledger, AST/subtest registry, count-based
  approval gate, and five bulky source-capture harnesses. Preserve unique hash,
  UTF-8 filename, analysis-mode/usage, and diagnostic regressions in direct Go
  tests alongside existing safety/orchestration coverage. Keep all 19 strict
  SDK-backed recordings and reused-asset provenance unchanged. Offline tests
  do not replace material-difference review or authorized host/platform/live
  validation. Earlier capture counts below are historical, not completion gates.
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
- Map one exact analyzer-copy binding parameter to two complete source
  observations, four binding hashes and a copy-operation semantic reference.
  Clarify dev/prod are analyzer IDs, not profiles, and preserve them across 18
  SDK-backed named/positional/output variants. Compare full ordered GET models/
  404s, full same-resource copy request/strict no-replacement POST and complete
  ready result/exact integer/output. Existing destination or missing source
  must prevent copy POST and expose full typed service errors/status/hints.
  Reject account discovery/client switching/grants/prompts. All native HTTP/
  results/errors are synthetic; no new recording or live/cross-resource proof.
  Totals: 138 evidenced, 659 pending; differences and both live-only copy
  scenarios remain unapproved/pending.
- Map one exact shared-validate parameter to both complete original Path
  bindings/types/four hashes and 45 native offline variants. Observe actual
  parsed schema arguments/flags while delegating unchanged validation; verify
  the exact spaced path before fixture-only source-path projection. Compare
  full clean/warning results, strict-warning/malformed/read errors including
  category/message/hint/code and empty quiet/error output. Prove zero service
  sessions, ignored service/auth environment markers, unchanged inputs and no
  extra files. Synthetic native cases are not Python validator/service parity.
  Totals: 137 evidenced, 660 pending; differences remain unapproved.
- Map one exact shared analyzer-test core parameter to both full source
  bindings/types/four hashes and 12 SDK-backed native variants. Preserve
  invoice-v1 and parsed default concurrency 4; verify 12 source-only
  argument variants fail sample discovery before session creation.
  Explicitly add one synthetic sample for positional/--file execution; compare
  full binary request/202 operation location/immediate-success result, SDK
  envelope and complete saved/stdout reports with narrow verified path
  projection and separate quiet output. Check reader closure, unchanged input,
  exact integer/confidence/null/statistics/disclaimer. Synthetic data does not
  prove valid-PDF/accuracy, four-worker performance or live/host equivalence.
  Totals: 136 evidenced, 661 pending; all differences remain unapproved.
- Map one exact shared-create core parameter to both original name/schema
  bindings, dictionary/path types and four hashes. Verify 12 original-name
  rejections before session creation; explicitly use synthetic invoice_v1 for
  12 successful SDK-backed variants without relaxing custom-ID validation.
  Verify spaced schema paths/both flags, full definition/no-replacement PUT,
  exact integer, complete immediate-ready result/output and empty quiet output.
  Native schema/results are synthetic; ID/path/type/host differences remain
  unapproved. Totals: 135 cases evidenced, 662 pending; no new cassette or
  live creation.
- Map one exact shared-delete core parameter to both source bindings and 12
  SDK-backed native name/positional/alias/output variants. Reuse analyzer-form
  and name-observer helpers; verify full GET then empty-204 DELETE order,
  complete name projection, full deletion acknowledgement and no prompt under
  explicit confirmation. Keep native outputs synthetic and type/surface/host
  differences unapproved. Totals: 134 cases evidenced, 663 pending; source
  variants are not counted as additional source cases.
- Map five exact core analyzer-show binding cases to seven complete original
  input/result/type/error observations and 45 SDK-backed native command variants.
  Verify trimmed names, full synthetic results, separate quiet output and
  zero-activity conflict/missing-name rejection. Preserve source type/surface
  facts and explicit safe native error differences without claiming Python
  implementation identity, source HTTP recording or live/installed-host parity.
  Totals: 133 cases evidenced, 664 pending; all five differences are unapproved.
- Map 31 source capability-disposition manifest cases to immutable original YAML
  and exact ordered ID/source-path/target/classification contracts. Verify
  classification counts and uniqueness; reject path/alias/target/identity drift
  with 24 controls and checked row references. Keep review metadata separate
  from source facts. This is structural evidence, not runtime/host workflow
  equivalence or approval. Totals: 128 cases evidenced, 669 pending; prior
  assets, capability/scenario review states and all approvals are unchanged.
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
