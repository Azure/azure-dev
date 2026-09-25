# Evaluations and dataset release testing

Use this contributor checklist for every release bug bash of `azure.ai.evaluations`
and `azure.ai.dataset`. One testing coordinator owns this document, the regression
inventory, and the evidence index. Individual testers supply receipts rather than
maintaining competing release plans.

The [bug-bash instructions][recipes] remain the command recipes and user journeys.
The [feed release index][release-index] identifies published packages. This guide
defines coverage, prerequisites, acceptance criteria, and evidence boundaries; it
does not replace either document or authorize publication, Azure spending, or
changes to shared resources. The feed is an unofficial prerelease feed, not the
official extension registry.

## Evidence contract

Use exactly `PASS`, `FAIL`, `BLOCKED`, or `NOT RUN` for each scenario on each tested
artifact. `PASS` requires measured assertions, not just command completion.
`FAIL` requires an observed mismatch; an unavailable prerequisite is `BLOCKED`.
`NOT RUN` means no execution is claimed. Record individual cases separately.

Every release receipt must contain:

| Field | Required evidence |
| --- | --- |
| Identity | Release tag, both extension versions, immutable source SHA, registry URL and SHA256, platform archive SHA256, installed executable SHA256, and `azd version` |
| Environment | OS and architecture, isolated configuration/work directory, native authentication method if used, and tester-owned resource prefix |
| Scenario | Stable case ID, prerequisites, exact sanitized commands, start/end time, timeout, expected assertions, and observed exit codes/stdout/stderr |
| State | Before/after authored and private file digests for mutation-sensitive cases; exact owned service identities and versions for live cases |
| Result | Evidence status, assertion results, redacted log/report links, relevant bug IDs, and limitations |
| Cleanup | Owned processes stopped, local state retained or removed intentionally, and owned service cleanup outcome; report cleanup failures separately |

Keep raw prompts, responses, tokens, and infrastructure identifiers in appropriately
restricted evidence, not public receipts. Remove URL user information, query
strings, and fragments before printing or publishing diagnostics. An installed
binary must match its verified archive member. Source tests, HTTP/gRPC fixtures,
hosted CLI checks, interactive terminal checks, and live service results are
different evidence types and must never substitute for one another.

## Isolation, cadence, and spending

Each of the six ongoing testers owns a separate `AZD_CONFIG_DIR`, scenario
directory, artifact/coverage ledger, and resource prefix. Never mutate the normal
global registry/configuration or another tester's agents, runs, files, or sessions.
Follow the [repository testing rules][agent-rules] for `NO_COLOR=1`,
`AZD_FORCE_TTY=false`, and `AZURE_DEV_COLLECT_TELEMETRY=no`. Set these variables in
the same process environment as the tested commands.

The six roles are practical, edge-case, docs-only novice, docs-only adversarial,
stateful, and metamorphic. The practical/edge pair covers realistic journeys,
negative inputs, recovery, automation contracts, and confusing UX. The stateful
tester checks sequences and recovery; the metamorphic tester checks invariants
across equivalent inputs and environment variations. Keep the docs-only pair
free of source and prior-finding hints.

Each maintains a replenishing backlog and runs back-to-back bounded, novel
current-package batches under the latest explicit user authorization, with an
owned native five-minute watchdog. Verify the saved schedule by reading it back.
A configured schedule proves neither that a scheduled invocation occurred nor
that any test passed.

At each batch, resolve Latest afresh, persist its immutable identity, compare
previous artifact/coverage, and select useful new scenarios or regressions.
An unchanged digest is not an idle condition: replenish local cases rather than
repeat the same assertions. Coordinate ownership so batches do not duplicate
coverage or overlap stress work. Use bounded batches and native watchdogs, not
an unbounded shell loop or broker inbox polling. Pause when requested, when no
safe novel work can be identified, or when execution becomes unsafe. These
cadences do not authorize repeated costly cloud cases.

Scheduled cycles are **local/offline by default**. Before a live case, obtain an
explicit authorization record covering the existing project, resource owner,
native authentication/access, existing deployments, unique owned assets, maximum
generation jobs/rows/conversations/turns/runs, spending limit, timeout, and cleanup
scope. Historical grants do not transfer to a new tester or release. Do not copy
tokens, create infrastructure/IAM grants, mutate a shared agent, broaden trace
queries, or retry known generation-count/deletion failures to rediscover them.
Mark missing live authorization `BLOCKED` and continue useful local coverage.

## Release gate sequence

1. Freeze one coherent source SHA and approved package/version tuple. Individual
   PR heads are inputs, not a combined candidate.
2. Build immutable local packages with provenance and checksums. Verify all
   archive layouts/manifests/entrypoints and required core version.
3. Independently install those exact bytes and run the changed-area Windows
   acceptance cases. Require measured results and explicit cleanup evidence for
   the agreed local/live gate. Unrelated optional coverage does not silently
   become a release blocker.
4. After explicit publication approval, publish non-Latest assets and verify
   anonymous downloads. The existing hosted-proof owner then consumes the
   actual public immutable registry and archives.
5. Require the agreed exact-candidate hosted CLI and source-race results, including
   independently downloaded reports and installed-byte identity checks. Only then
   approve Latest promotion and verify the rolling URL with a fresh installation.
6. Update this inventory, the version-specific receipt, and installation pins
   together. Retain old receipts without relabeling them as new executions.

**Historical build 42 scope decision:** prioritize a smaller package of independently
validated fixes and defer changes needing live proof. The intended baseline is
build 41 plus the proven cancellation correction, with an isolated race fix only
if independently validated and explicitly included by the integration owner.
The immutable inclusion map is authoritative. New simulation-contract,
qualified-model, and nested-seed changes are deferred, not implicitly accepted
because their source tests pass. This decision authorizes no live generation or
spending and closes no bug by deferral.

For the smaller candidate, retain the existing four offline hosted jobs: actual
installed CLI on `ubuntu-24.04` and `windows-2025`, plus the evaluations and dataset
full internal race suites on Linux with Go 1.26.4:

```console
go test -race -count=1 -timeout 15m ./internal/...
```

The baseline is **160 unique actual CLI commands per OS**: 124 existing checks
plus 36 dataset-binding checks. Preserve their assertions when an intentional
contract change requires updating input fixtures; a count alone is not coverage.
The binding matrix is four configuration layouts by three modes by three
behaviors: collision refusal, same-file reuse, and distinct-file addition.

The 50 seed-refusal checks include four cold-entry cases that may create only
the normal zero-byte `.azure/.env.lock`; the other 46 must remain unchanged.
The same exact lock allowance applies to 12 cold-entry binding refusals. Do not
replace digest comparisons with broad ignored directories. Packaging six
platforms does not establish macOS/ARM runtime coverage. Offline hosted checks
do not establish terminal interaction, live Azure evaluation, or cloud quality
gates. Scenario CI implementation in GitHub Actions and Azure DevOps is now
authorized as a separate effort. Actual service-backed execution still
requires an approved identity/resource/budget tuple; pipeline implementation
does not grant that permission or add a new blocker to the smaller release.

### Deferred contract-expansion plan

The earlier conditional 176-case proposal is **deferred, not a build 42 gate**
when the new contract is excluded. Retain these proposed expectations for a
future explicitly scoped candidate; do not change build 41-compatible fixtures
merely because the release number becomes 42. The proposal preserves 160 case
intents and adds at most 16 checks per OS in the same four jobs:

| Planned delta | Cases per OS | Expected contract |
| --- | --- | --- |
| Bare simulator model, fresh/existing state | 2 | Reject a model without `connection-name/model-deployment` qualification. |
| Top-level `desired_num_turns`, fresh/existing state | 2 | Reject; numeric seed settings belong inside `simulation_configuration`. |
| Nested desired turns 21 with omitted global limit, fresh/existing state | 2 | Reject against the effective default ceiling of 20. |
| Description length 2499/2500/2501 in ASCII, accented text, and emoji | 9 | Accept 2499/2500 Unicode code points and reject 2501; do not count UTF-8 bytes. |
| Non-object `simulation_configuration` | 1 | Reject clearly before authored writes. |

Keep valid nested references in the existing positive matrix. The former valid
21-turn control must explicitly provide a nested per-row `max_num_turns: 21`;
an omitted global `max_turns` must stay absent in authored YAML. Confirm this
against the combined init and run paths, not a PR tree that lacks integration
files. No broad exclusions or replacement of existing whitespace/mixed-row
guards are acceptable. The plan is at most 176 CLI cases per OS, not a new
platform matrix.

Wire payload/mapping, canonical dataset identity, generated-row normalization,
metadata fallback, and pre-worker SDK initialization have separate source-test
obligations. Mapping retrieval does **not** establish full configured-model/default
preservation on simulation rerun. Job reattachment metadata proof is also not
that proof. Full live model/default rerun preservation remains `NOT RUN` until
directly measured; it is not silently added to an approved paid test envelope.

## Scenario inventory

Execute commands from the pinned [bug-bash recipes][recipes] using the installed
package's `--help`. Supply observed names/versions instead of guessing identities.
Record the exact expanded command in the receipt. These are coverage obligations,
not a claim that every combination is implemented or tested.

| ID / owner | Prerequisites and commands | Required assertions |
| --- | --- | --- |
| PKG-01 / both | Fresh isolated configuration; `azd extension source add`, exact-version `extension install`, `extension list --installed`, `ai eval version -o json`, `ai dataset version -o json`; repeat through rolling Latest | Registry/source/version/core/platform/archive/executable identities agree. Adding a source alone must not be mistaken for upgrading installed bytes. JSON parses without console noise. |
| UX-01 / practical | `azd ai --help`; evaluation init/create/run/output help; dataset/evaluator create/update/version help | User can discover the installed commands and follow next steps. Agent management is a separate official extension, not implicitly supplied by this feed. Help-only checks do not prove a workflow. |
| INIT-01 / practical | Valid static, turn, and simulation fixtures; `azd ai eval init ... --no-prompt`; custom configuration and nested references | Correct mode/model/dataset mapping, add-only edits, existing metadata/pins/references retained, and no unexpected default sidecar. Init is not generation, publication, or a run. |
| INIT-02 / edge | Invalid, empty, mixed-shape, or malformed seed files; custom paths containing spaces; `init` with JSON/no-prompt and real terminal correction/cancel | Reject invalid local inputs before authored/private writes; preserve exact declared-file resolution. Validate blank descriptions, zero/fractional/negative desired turns, explicit limits, and conflicting flags. Allow only the documented core lock exception. |
| LIFE-01 / practical | Authorized owned assets; `dataset create/update/versions list`, `eval create`, repeat unchanged, edit file and repeat | Service-issued versions match authored intent; unchanged repeats are idempotent; dataset changes select the new canonical version without losing earlier run history. No invented dataset identity or silent inline fallback. |
| RUBRIC-01 / practical | Owned custom rubric; evaluator download/edit/create/versions list, then unchanged repeat and remote-ahead conflict | Download is editable in the documented shape; dimensions/metadata survive round trip; only intended versions change; remote edits are not overwritten silently. Distinguish rubric changes from immutable eval-contract changes. |
| RUN-01 / practical | Approved one-row static or bounded conversation case; `eval run start`, `run show`, `output list/show/export` | Inspect actual terminal execution status, output rows, returned evaluator results and raw field preservation. A completed quality failure is not an execution failure. Missing values are not inferred as zero or false. |
| RUN-02 / edge | Existing owned failed run or bounded approved failure; follow printed inspection/export commands | Immutable eval/run IDs resolve; unfiltered output remains available for inspection even with zero rows; error diagnostics and exit codes remain actionable. Do not create paid failures solely for coverage without approval. |
| AUTO-01 / edge | No-prompt/JSON combinations; accepted run with `--no-wait`; later `run show --wait`; `--fail-on`; JSON export | No prompts or dropped explicit flags; valid single JSON output where promised; usable returned IDs; breached gates exit nonzero. Export file extension does not change the JSON format. |
| CANCEL-01 / edge | Actual Windows terminal picker plus in-progress owned command with bounded cleanup | Distinguish explicit picker Cancel from Ctrl+C and record the delivery method and exit code. Redirected stdin or a timeout is not interactive terminal proof. Test service cancellation separately from local picker cancellation. |
| DATA-01 / practical | Owned versioned single-file and multi-file datasets; downloads in both namespaces with file/directory output | Bytes match source; overwrite requires explicit consent; forced replacement works; relative layout preserved; a one-file folder is not automatically a single-file dataset. |
| EDGE-01 / edge | Bounded local fixtures or authorized service cases for empty pages, pagination, malformed data, missing identity, retry/timeouts, partial failure | No cross-project/version mix-ups, duplicate/missing page results, silent success, dropped errors, runaway retries, or secret disclosure. Label fixture proof separately from live behavior. |
| PRECISION-01 / edge | Owned loopback fixture through affected typed detail/list-page/file-JSON paths, with raw service export as a separate unaffected control; baseline and repaired artifacts identified separately | Preserve integer `9007199254740993` and decimal `0.123456789012345678901234567890` as numbers, not strings, with an exact oracle. Public42 Linux extension detail is now measured `FAIL` under the explicitly authorized local SDK/auth mocks described below. Remaining typed paths and repaired-package coverage are `NOT RUN`; this is not live/full-core authentication proof. |
| RECOVER-01 / both | Owned partial publication/generation state; rerun/reattach and explicit cleanup | Persisted identity is reusable, retry is idempotent, partial success is reported, cancellation stops owned work, and cleanup matches actual service delete scope and response contract. |
| TRACE-01 / practical | Explicitly authorized owned agent/version, trace access, and narrow request window | Returned traces belong to the intended owned agent/version/window. Do not widen to shared traffic when ingestion or permissions block the case. |

For a future approved qualified-simulation acceptance case, use an **existing connection and model
deployment pair**, a separately selected judge, and the recorded target
agent/version. Do not infer the qualified simulator from a bare deployment name.
The approved workflow is generate/collect, init with nested seed configuration,
explicit create/publish, run, and reattach. Require a new canonical dataset version
identity, the configured qualified model, terminal results, and preserved metadata.
Local HTTP/gRPC fixtures cannot satisfy that live acceptance case.

The proposed producer check is **not** a one-sample generation request:
the inspected source accepts `generate --max-samples` only from 15 through 1000
(zero selects the default 15). Conversation generation uses `simulation_seed`,
not `simple_qna`, on the same API collection; shared backend implementation is
not established. If explicitly approved, select dataset-only, prompt-only
generation so the test does not also submit a rubric job or agent fallback.
After collection, explicitly publish one reviewed canonical seed row with
per-row maximum turns at most 2 and repetitions 1 before the one-run check.

Neither the requested generation count nor two simulation turns is a hard
token/currency ceiling. The inspected CLI exposes no such ceiling; timeout,
Ctrl+C, polling deadlines, and best-effort cancellation do not prove that remote
billed work stopped. Do not label an observation deadline or an alert as spending
enforcement, blindly resubmit an ambiguous POST, or equate deletion of a job
record with deletion of generated artifacts. A hard monetary budget remains
blocked until an authorized owner supplies a verified service-side control.
This live producer/contract expansion is deferred from the smaller build 42
scope, not a prerequisite that may silently delay that package.

## Required bug regressions

Carry all seven entries into **every** release receipt, plus every subsequently
accepted defect. A closed work item does not remove its regression case. The
existing ADO owner read these seven records using native authentication at
**2026-09-24T01:54:56Z**. Priority and severity are distinct service fields. Refresh
them before filing, reopening, or closure; this is a timestamped receipt, not
continuously refreshed state. All seven had no human assignee, which does not mean
there is no agent working on a linked fix.

| Work item | State | Priority | Severity | Revision |
| --- | --- | --- | --- | --- |
| [5640927][bug-5640927] | Done | 1 | 2 - High | 2 |
| [5631330][bug-5631330] | New | 1 | 3 - Medium | 1555 |
| [5595070][bug-5595070] | Done | 2 | 3 - Medium | 5 |
| [5595119][bug-5595119] | New | 2 | 4 - Low | 2 |
| [5571322][bug-5571322] | Done | 3 | 4 - Low | 4 |
| [5572139][bug-5572139] | New | 2 | 3 - Medium | 9 |
| [5530209][bug-5530209] | New | 2 | 3 - Medium | 16 |

`Done` is the project's completed-category terminal state. The [closure
receipt][closure40] applies to the original issues, not every later behavior
change on the same commands.

| Work item | Expected regression / acceptance | Evidence boundary and next release disposition |
| --- | --- | --- |
| [5640927][bug-5640927], invalid simulation init | Invalid local/nested seeds fail before authored/private writes; valid controls and interactive correction still work. | Reported Done. Build 40 local/terminal proof and build 41 hosted seed checks exist. Repeat on each new package; do not equate source validation with installed behavior. |
| [5631330][bug-5631330], generation sample/cost cap, P1 | The actual service generation path honors the requested bound and billed work; client truncation is not a fix. | Reported New, backend ownership unresolved. Historical request 15 produced 16 on the reported path. New live reproduction is `BLOCKED` without explicit budget and a relevant service change. Client repackaging does not close it. |
| [5595070][bug-5595070], failed-run follow-ups | Failed/errored runs print usable immutable-ID inspection/export guidance; zero output rows remain diagnosable. | Reported Done. Build 40 has a naturally failed responses-backed run and executed follow-ups, beyond earlier synthetic HTTP proof. No blanket real errored-row or build 41 rerun claim. |
| [5595119][bug-5595119], agent guidance | The separately delivered agent artifact gives correct current evaluation/dataset guidance and usable commands. | Distinct artifact from these two packages; original source work is [#10042](https://github.com/Azure/azure-dev/pull/10042). The owner reports the targeted deploy-note unit test, production build, and isolated local install/version at `4780cd608b16a0ebe1b479e531ea08b66c096da9` passed. Actual deployed-note acceptance and public delivery remain unverified; no closure or lifted release hold is implied. |
| [5571322][bug-5571322], cancellation | For the corrected picker contract, Ctrl+C exits 1 and explicit Cancel exits 0, with no unintended mutation; ambiguity in no-prompt/JSON exits nonzero. | Original item reported Done. The later [#10113](https://github.com/Azure/azure-dev/pull/10113) development artifact has separate terminal proof and is not build 41. Repeat against the combined new package; true Windows console-signal delivery is a separate unverified case. |
| [5572139][bug-5572139], terminal generation-job deletion, P2 | Supported deletion of an owned terminal job succeeds with correct empty-response handling and local cleanup. | Reported New, backend HTTP 409 remains unresolved. No verified retention TTL or client-package fix. Do not loop DELETE against known failures. |
| [5530209][bug-5530209], editable rubric shape, P2 | Download/edit/republish preserves `type`, `dimensions`, `pass_threshold`, declaration metadata and version behavior; unchanged repetition remains stable. | New residual tracked with [#10148](https://github.com/Azure/azure-dev/pull/10148). Earlier metadata/version passes through create/reconcile do not establish the complete editable round trip or live `azd up`. |

Additional linked checks discovered during coordination remain separate from new
bug filings:

| Existing item | Evidence and required follow-up |
| --- | --- |
| [5572011][bug-5572011], stale local evaluation state after remote deletion | Scoped deduplication matched the peer-audit observation to this existing Done item, revision 13, priority 2, severity 3 - Medium, and comment 8226717. This is not a new P3 bug or the terminal-job HTTP 409 issue. After the audit, the existing lifecycle owner reported a local recurrence and accepted a minimal fix with command tests; repaired installed-package acceptance and a fresh work-item state receipt remain pending. Reconciliation already recovers after service 404. |
| [5631310][bug-5631310], built-in evaluator validation | Existing best-effort catalog behavior is already in build 41. Exact-package init checks for invalid explicit IDs, a valid ID outside the initial offered choices, and offline preservation remain queued. Network/auth/empty-catalog fallback is not authoritative offline ID validation. Do not substitute create/up or source CI for the init cases. |

New installed-package regressions were subsequently filed and read back by the
authorized issue owner. At initial filing both were New, priority 2, severity
3 - Medium, revision 2, classified as internal testing and human-unassigned.
The later exact43 closure receipt below supersedes that state snapshot, not the
original reproduction. Their original artifact identity is
the exact public [build 41 package][build41] and registry/executable hashes
recorded below, not the unpublished build 42 package.

| Work item | Required per-release regression | Current disposition |
| --- | --- | --- |
| [5645048][bug-5645048], new custom YAML path becomes a directory | With a previously nonexistent filename passed to init `--path`, verify the promised file/directory interpretation, actual file type, selected configuration, and reattachment. File existence or a matching substring alone is not sufficient. | Original public41 defect retained. Exact required-only public43 acceptance completed; authorized owner reports Done/revision3/comment8298473. Not a build42 repair. |
| [5645047][bug-5645047], unsupported init output format is ignored | Explicit unsupported output formats must fail clearly, with correct exit/output and no unintended authored changes. Do not equate an unsupported-format observation with an unproven file-mutation claim. | Exact required-only public43 acceptance completed; authorized owner reports Done/revision3/comment8298470. Source work remains linked through [#10149](https://github.com/Azure/azure-dev/pull/10149); build42 did not include it. |
| [5645136][bug-5645136], explicit empty required flags | Explicit empty required authoring values must be rejected rather than treated as omitted defaults. | Exact required-only public43 acceptance completed; authorized owner reports Done/revision2/comment8298472. Keep the regression in future matrices. |
| [5645220][bug-5645220], multiple YAML documents lose content | Reject unsupported multi-document input clearly rather than silently discarding a document; preserve the original file on refusal. | Reported filed; outside the current43 inclusion map. No repair or closure claim. |
| [5645284][bug-5645284], trailing JSON reference content | Refuse extra trailing JSON content at a reference boundary rather than accepting only a valid prefix. | Reported filed; outside current43. Independent repaired-package acceptance remains pending. |

The separate initial mutation claim was disproved by its snapshots and was not
filed. Keep that correction with the original observation; do not coach blind
testers with the source diagnosis or treat source fixes as closed regressions.
The reported [source repair head][init-source-repairs] also contains the
explicit-empty-evaluator correction tracked in5645136.
That main-based development artifact includes deferred simulation contracts;
it is neither repaired feed 41/42 proof nor an automatically approved next
feed candidate.

The high-impact numeric-precision regression is required for every relevant
future package matrix. Existing [5571358][bug-5571358] was reopened as Active,
priority 2, severity 2 - High, with source work associated with
[#10147](https://github.com/Azure/azure-dev/pull/10147). The exact42 source fixture
failed before the precision fix and passed afterwards; that is not public-EXE
runtime proof. Raw service export bypasses the affected typed decoder and is an
unaffected control, not a blanket data-loss claim. `PRECISION-01` is assigned
to the existing edge lane, not a new investigator. Baseline public42 and any
future repaired package need independent installed-byte receipts. No live cost,
TLS bypass, or global trust changes are authorized for the loopback fixture.
The edge lane's exact integer/decimal/raw-token oracle and ordinary, rounded,
quoted and malformed controls passed **oracle-only** checks. Subsequently, the
real public42 Linux extension ELF reproduced the detail-path defect through an
explicitly authorized local SDK gRPC/auth-subprocess mock with normally verified
TLS. The served unrounded payload SHA256 was
`384f75320a9993ebef99c600e523602961e5fc58fbc0290f66c22665e316daa6`.
Both signs of `9007199254740993` rounded to `9007199254740992`; the long decimal
became `0.12345678901234568`, while an unknown raw integer field was preserved.
The unchanged ELF SHA256 was
`bf13bab80679aa1da842906ade4bf86b1ec5e8c12a363d755bbd2b88384e51d7`.
Two API requests and three loopback connections were observed, with no external
connection and all owned servers stopped. Negative TLS/proxy/RPC controls passed.
The first unknown-RPC guard failure was retained; the second attempt used the
source-verified SDK namespace. This is actual published-extension local-mock
runtime evidence, not live Azure or full-core authentication. At that detail
checkpoint, typed list/file, raw/error controls and fixed43 were not yet run;
the subsequent scoped local43 receipt is recorded in the release index below.
The published binary was not instrumented or replaced with a repair build, and
no blanket fixed-package or issue-closure claim follows.

For new defects, first deduplicate in the relevant project against these records
and known linked fixes. File through the authorized [evaluation bug channel][bug-channel]
with impact-based severity, exact identity, reproducible commands, expected/actual
behavior, redacted evidence, and ownership boundaries. Confusing or misleading UX
is a valid finding even without a crash. Do not assign arbitrary people. If filing
is unavailable, retain a filing-ready receipt marked blocked rather than inventing
a bug ID. Route it to the fixes owner immediately and independently verify the
repaired **installed package** before recommending closure.

### Required-only43 closure snapshot

The authorized work-item owner completed closures and an independent read
confirmed them at **2026-09-24T07:45:29Z** after exact public067/reg4027
acceptance and release/CI evidence. Only these four items are covered:

| Work item | Observed final state | Revision | Comment |
| --- | --- | --- | --- |
| [5571358][bug-5571358] | Done | 12 | 8298471 |
| [5645048][bug-5645048] | Done | 3 | 8298473 |
| [5645047][bug-5645047] | Done | 3 | 8298470 |
| [5645136][bug-5645136] | Done | 2 | 8298472 |

Actual assignee, priority and severity were preserved. Supplementary blind
replays add no release gate or automatic reopening/closure action. The separate
5572140 concern, excluded lifecycle work, and backend retention remain outside
these closures. Keep all regression cases in future matrices.

## Release evidence index

Snapshot: **2026-09-24**. These links describe their named immutable release only.
The current fresh Windows checks do not replace the historical broader matrices.

| Release | Identity and receipt | Status and limitations |
| --- | --- | --- |
| 40 | [Build 40 receipt][build40], evaluations `1.0.40-beta`, dataset `1.0.0-beta.28` | Historical scoped local/terminal/live results, including the responses-backed execution `FAIL`. Do not relabel as 41. |
| 41 | [Frozen build 41 receipt][build41], evaluations `1.0.41-beta`, dataset `1.0.0-beta.29`, source `8ef8b6df77336950c60506ab2966037f579d92cd`, azd `>=1.33.0` | Historical release, previously Latest. Targeted package acceptance and four hosted jobs `PASS`; separately, the new scenario run resolved this release before promotion and passed 168 checks per OS. Broad live reruns, all platforms, and all seven regressions are not claimed. |
| 42 | Public [release `extensions-2026-09-24-42`][release42], evaluations `1.0.42-beta`, dataset `1.0.0-beta.30`, core `1.33.0`, frozen source [`d40a3b5a1e7c5944b1b43decd14c96096a99e5b6`][source42]. Registry SHA256 `83026575746f7db5c5cc7a3035f9e75b776f709875d2f0768bd9c215aaaca635`. | Previously Latest following approval at **2026-09-24T03:17:39Z**. Publisher verified all 15 anonymous assets, a fresh unversioned install and stable registry/docs at [feed revision `967b631`][feed42]. Both local Windows scopes and all four [hosted jobs][hosted42] remain `PASS`; both OS artifact sets were downloaded/verified. Historical proof is unchanged by43 promotion. |
| 43 superseded local proposal | Historical source [`2d4ea18150b06cd6ecc7a31998069f712bf874f9`][source43], proposed evaluations `1.0.43-beta` and dataset `1.0.0-beta.31`; local registry SHA256 `21c176596c731c025bc8124e36e00831348188e22109856358904c2aa0c0c7fb`. | **Superseded, partial local evidence only; never a public tuple or aggregate PASS.** Do not pin, dispatch or transfer its per-fix PASS results to the required-only successor. New exact source, hashes, acceptance and public metadata are required. |
| 43 required-only public release | [Release `extensions-2026-09-24-43`][release43], evaluations `1.0.43-beta`, dataset `1.0.0-beta.31`, core `1.33.0`, source [`067b2fd5622494db2dc6ac3a2e9bc19e871ed9e7`][source43-required]. Registry SHA256 `4027cd85bf2a5853db90b4bed12c225eb125197e758e3015a88f9e9aca7a3215`. | Latest promotion approved at **2026-09-24T07:31:22Z**. Publisher verified all15 stable/pinned assets, fresh unversioned Windows installation, root registry and docs at [feed revision `0f49946`][feed43], with all90 historical asset identities preserved. Fresh required-only local acceptance and all four [hosted jobs][hosted43] are `PASS`; both OS artifacts downloaded/verified. Excluded lifecycle fixes remain unaccepted. |

For those superseded local bytes, the original path/format/empty-flag Windows
scope passed three cases, 38 assertions and ten commands with owned cleanup;
the coordinator independently checked both installed executable hashes. A
six-assertion/two-command literal-empty supplement is recorded separately.
The Linux precision scope passed seven candidate paths/controls against the
measured public42 typed-decoder failures using the authorized SDK/auth-subprocess
local mocks and normally verified TLS. Coordinator corroboration adds no cases.
This is not live Azure or full-core authentication.

Phase-C rubric metadata, first-publication, and delete-cleanup acceptance
([5530209][bug-5530209], [5645184][bug-5645184], [5572011][bug-5572011])
did not reach whole-lifecycle acceptance for this proposal. The current
inclusion map is authoritative; superseded source preparations are not evidence
for the selected bytes. No broad new model/simulation contract is inferred.
After the phase-C deadline, Track1 selected a required-only successor based on
build42 plus precision, path/format and explicit-empty-flag fixes. Rubric and
delete-lifecycle changes are excluded from that fallback. The historical
local2d4/reg21c receipts remain available, but no prior acceptance carries over
to the new bytes. The required-only public receipt above follows fresh
acceptance, not a relabeling of that proposal.

A later bounded state-free study ended with six actual CLI cases: five complete
and one capture gap. Public067 first-publication remained `RED`
(one GET, zero POST, exit1), and a bare metadata update omitted four fields.
The unpublished2d4 development binary captured an absence guard
(six GET, zero POST, exit1), four-field inheritance and explicit-empty override.
Its first-create case remains `INCOMPLETE_CAPTURE` after Decimal receipt
serialization; no PASS or rerun is inferred. Testing reported the bounded
1028.785-second duration and owned loopback/process guards. This is not stateful
SDK/delete/reconcile/native-up proof, release acceptance or issue closure.
Any capture-recovery execution needs its separately agreed scope.

The exact required-only43 hosted run is **35969288265**, head
`e8d07ded9163562976a14b9eb9b61952829bd36e`. Both manifest source fields pin067b;
the workflow, harness and ordered check-ID blobs were frozen at dispatch.
Both downloaded OS sets verify160 canonical commands/outcomes, 50 seed refusals
(four exact new zero-byte locks and46 unchanged snapshots), 36 binding checks
(12 strict refusals and24 preservation cases), runtime versions, all public
archive pins and successful cleanup. Both source-race logs verify Go1.26.4 and
the unreduced command on exact067b. This is not live Azure, interactive CI,
excluded lifecycle acceptance, or the separately triggered Latest-configuration
scenario run.

The local build 42 practical receipt covers six cases and 25 assertions: three
init/reattachment journeys and three help-only cases. Its 16 records include
14 azd commands and two Go metadata checks, including one preserved input typo
and its correction. Help checks are not runtime create/run/export proof.
The edge receipt covers eight first-attempt cases across create/run-start:
ConPTY byte `0x03` exits 1, explicit Cancel exits 0, and no-prompt/JSON ambiguity
exits 1. JSON-mode terminal checks produced one error object and empty stderr.
Owned processes and fixtures were cleaned, no denied network connection was
observed, and file state remained unchanged except the allowed empty core lock.
Both testers used fresh isolated configurations and independently rehashed both
installed executables against the local tuple. The coordinator's receipt review
adds no cases. This is not Windows `CTRL_C_EVENT`, public installation, live
service, hosted CI, or overall release proof.

The separate final hosted build 42 run is **35949654046**, workflow revision
`421a464525f280bb63dc0c69a9c52f7a852b5cd3`. Its workflow and harness are
byte-identical to the build 41 gate; only candidate source/version/release/hash
pins changed. Both source fields pin `d40a3b5a1e7c5944b1b43decd14c96096a99e5b6`.
Downloaded Linux and Windows artifacts each retain all 160 unique command
identities/outcomes, 50 seed refusals (four exact zero-byte cold locks and
46 unchanged snapshots), and 36 binding checks (12 strict refusals and
24 preservation successes). Both full Linux race logs confirm Go 1.26.4 and
the unreduced command above. This satisfies the agreed hosted candidate gate;
it does not turn local terminal evidence into hosted interactive/cloud proof.

Build 41 registry SHA256 is
`aff0d6f456e3fb08773b1c888136eed12ec8a06bd2eba0addda0383142ae7d79`.
Its Windows amd64 installed executable SHA256 values are evaluations
`ce8b8906a52f9879470ace66daab4edf71795d0566bd45243271eed9c54d0255`
and dataset
`43b6223ecb3d2702f3d00c0731e1ad8f758800b940971a5fe9050b18d419cbf5`.
Use the release's linked checksum/provenance assets for all archives. The
[frozen hosted run][hosted41] passed 160 actual CLI checks on each tested OS and
both full source-race suites; it is not evidence of build 42 execution.

The new practical tester's first build 41 cycle passed 14 local assertions:
valid static and simulation init, custom paths containing spaces, distinct-judge
reattachment without `--path`, original configuration-prefix/root/dataset byte
preservation, duplicate-eval refusal, and reporting/export help interpretation.
Model and agent names were offline fixture identifiers. This proves local
authoring and help, not deployed model validity, remote reattachment, actual
grading, quality-gate exit behavior, or exported run results.

The new edge tester's first build 41 cycle independently passed one malformed
local-seed case under `INIT-02`: actual installed `init` with simulation mode,
custom paths containing spaces, `--no-prompt`, and `--output json` returned exit
1 and one `error.message` JSON object with empty stderr. Every existing file
remained byte-identical, no evaluation configuration/sidecar was created, and
only the documented zero-byte `.azure/.env.lock` appeared. The original harness
incorrectly expected empty stdout and no lock; the retained observation was
assessed against the documented contract without a command rerun. This is not
a newly discovered product defect or a reason to reopen 5640927.

## Overnight scenario CI and docs-only testing

The new work baseline is **2026-09-24T02:15:05Z**. Earlier build 41 checks,
schedule configuration, peer audit, and document commits are existing evidence,
not newly completed overnight scenarios. Record only subsequent cases, findings,
fixes, retests, package/doc versions, implementation changes, and actual run links
as new progress.

### One scenario implementation, two CI systems

The existing hosted-proof owner also owns one coherent scenario-CI contribution
with shared fixtures for GitHub Actions and Azure DevOps. Keep the smaller
release's frozen workflow, fixture contract, and pins separate from this new
work. Do not create a different runner or owner for each CI provider.

| ID | Required behavior and evidence | Current execution status |
| --- | --- | --- |
| CI-01 | Resolve Latest once and freeze release/source/versions/registry/archive digests in the producer manifest. Each OS consumer derives executable digests from those verified immutable archives, compares installed bytes, and records both digests. Parallel jobs use the same resolved identity, not a moving Latest URL. | `PASS` in [scenario run 35950594406][scenario41]: producer/Linux/Windows manifest bytes match; executable digests are consumer-recorded and verified against archive members, not separately frozen by the producer |
| CI-02 | Execute actual no-prompt CLI authoring and recovery cases, checking commands, JSON, exit codes, errors, cancellation semantics, reproducible environment/configuration, and state preservation. Record limits of non-terminal cancellation checks. | `PASS` for 168 offline checks per OS: 160 retained plus six configuration-isolation commands and two cancellation-argument refusals; interactive/service cancellation remains `NOT RUN` |
| CI-03 | Publish sanitized command/assertion reports and artifact manifests with actual workflow/build/job links. Download and inspect the artifacts independently; successful YAML validation alone is not a CI execution result. | `PASS`: resolver pin and both OS artifact sets downloaded; commands, state, cleanup, runtime versions, archive/binary digests and blocked-live receipts verified |
| CI-04 | Run the shared offline scenario suite through the new GitHub Actions entry point with least-privilege permissions and bounded timeouts. Keep cloud-service scenarios separate. | `PASS` at `d6818868d81ee59b5782c0a0a12413d008cbb9ff`; live branch was not requested and is not counted as a pass |
| CI-05 | Run the same offline suite through Azure DevOps using an explicitly authorized existing organization/project/pipeline/repository connection and available capacity. Do not guess or mutate a shared pipeline to manufacture a run. | `BLOCKED`: YAML/local validation passed, but scoped native metadata discovery found no matching authorized scenario-pipeline tuple; no Azure DevOps execution claimed |
| CI-06 | Wire real create/evaluate/run/export jobs behind explicit identity, existing owned resource, budget, duration, and cleanup parameters. Missing prerequisites produce a clear `BLOCKED` or `NOT RUN` report, never a live-test success. | `BLOCKED` execution, implemented code: static evaluation plus an opt-in owned prompt-version/manual one-row dataset create/eval/run/export/identity-specific-cleanup sequence. Only mock validation is claimed. Infrastructure deployment, generation, auth/plan bootstrap and monetary enforcement remain `NOT IMPLEMENTED`; see the exact [executor matrix][scenario-guide]. |

The first new scenario producer resolved build 41 at
**2026-09-24T03:14:18.582604Z**, before build 42's Latest promotion. Its manifest
SHA256 is `1912ef0f221f0ce6f517dcd972d2cb7783ead701f89eff1a3c2a3841d3d9957b`.
Both OS consumers used those exact bytes and finished all 168 checks with
successful owned-workspace cleanup. This is not a build 42 scenario run merely
because Latest changed afterwards. See the [runner guide][scenario-guide] for
reproduction, artifact layout, authorization boundaries, and the distinct
fixed-candidate workflow.

The subsequent approved cleanup/arity quality round used
[scenario run 35951992378][scenario42] at
`bca776eecd1f3d697b524d3d2ed321999425aae7`. It resolved public Latest42 at
**2026-09-24T03:34:18.707908Z**, with manifest SHA256
`0fc0e1fe7f9c1d92c6a7859c198dc7a4fd10fdaf53e6e17ef62475082e561a22`.
Both downloaded OS receipts verify 168 checks and clean owned-workspace cleanup.
The paired [candidate-harness repair run][candidate-repair42] also passed;
neither replaces immutable release acceptance `35949654046`.
The publisher's later native Latest42 input event was deduplicated against this
already-completed matching identity/quality round, with no extra dispatch.
This proves native handoff accounting, not an operational GitHub release hook.

No exported developer credentials, new IAM grants, shared public-runner
registration, unapproved Azure spending, or generation jobs are authorized by
this implementation work. An offline job may pass while the accompanying live
case is blocked; keep both results visible. Publish real failures as failures,
not successful skips or fallback outputs. Retain the monetary-control limits
described above.

### Fresh black-box contexts

Two additional contexts test only intended public user docs and published
extension bytes: a novice, strictly procedure-following profile and an
advanced/adversarial profile. They are not forks of the investigation and are
separate from the two existing package testers. Preserve configured model
preferences and record each actual runtime model and profile independently;
profile contrast does not prove different model capability.

Their allowed product inputs are the public feed README, public bug-bash user
instructions, user-documentation pages those directly reference, the published
registry/packages, and installed help/output/metadata. Do not give them this
ledger, internal recipes, private issue lists, source code/tests, other agents'
findings, or solution hints. Record any mandatory safety/governance instructions
as a separate unavoidable input. Stage allowed inputs in owned directories;
instruction-based access restrictions are **not an OS sandbox**.

| ID | Required attempt or assertion | Evidence boundary |
| --- | --- | --- |
| BLIND-01 | Record allowed inputs, public doc revision/content hashes, actual model/profile, package identity, and access restrictions before testing. | No claim of fresh blindness if prior findings or implementation were consulted. |
| BLIND-02 | Follow discovery/install/setup/no-prompt authoring and the documented create/run/results/export journey within approved permissions. | Preserve original commands, observations, failures, and interpretation before recovery or coaching. Help is not runtime proof. |
| BLIND-03 | The adversarial profile tries plausible input/path/flag variations and recovery using only docs/help and safe local fixtures. | Distinguish product defect, doc gap, authentication/environment prerequisite, and mistaken user/model interpretation. |
| BLIND-04 | Send the original finding to the coordinator before historical-issue deduplication; independently repeat the same reproduction on repaired published bytes. | Do not rewrite initial evidence after receiving an explanation or leak the prior answer into the first attempt. |
| BLIND-05 | Persist coverage, replenish novel current-package cases, and execute back-to-back bounded batches with an owned five-minute native watchdog. | Read back schedule configuration; report actual invocations separately. An unchanged digest alone is not a reason to idle or repeat old assertions. Preserve the original docs-only input boundary. |

The latest explicit user override expands the pool to six roles: the existing
practical/edge pair, the docs-only novice/adversarial pair, and the new
stateful/metamorphic pair. It supersedes the earlier finite-sweep/change-only
30/60-minute tester cadence, not the identity and spending restrictions.
Coordinate bounded batches without broker inbox polling, duplicate workers,
overlapping stress, or repeated paid attempts. While the user is away, choose
safe local work and queue missing approvals instead of asking new questions.
Publication authorization does not authorize cloud testing or remove
exact-package acceptance requirements.

The testing coordinator confirmed saved five-minute watchdog configuration for
all six roles; configuration alone is not a test result. The first separately
observed novice watchdog invocation at **2026-09-24T02:44:52.494Z** executed six
new cases, nine CLI commands, and 27 assertions. Preserve that receipt separately
from the earlier novice sweep and schedule setup. The four fresh contexts report
the same `gpt-6-astra` runtime model; their distinct profiles are not evidence of
model-capability diversity.

Historical novice receipts 00-98 did not enumerate hidden entries. Their captured
authored/configuration/fixture bytes remain evidence, but they do not prove that
the entire hidden filesystem history was unchanged. Missing historical hidden
before-states were not reconstructed or retroactively retested. The corrected
future snapshot helper was actually checked in receipt 101 against a hidden
Git HEAD file; refusal 100 separately retained ordinary/hidden state. This
qualification does not alter the independently scoped practical/edge build 42
critical-gate snapshots.

For a later artifact that actually includes the fixes, the practical tester will
check the editable rubric shape, declaration metadata, version behavior, and
unchanged-repeat behavior. The edge tester will check local evaluation-state
cleanup after deletion by service ID, name, and configuration scope. A local
fake-service test is useful only if its evidence says whether it drove the
installed package or a source test. Do not relabel source fixtures as shipped
behavior, spend Azure funds to fill the gap, or add these unrelated fixes to
the smaller build 42 scope.

## Peer-extension parity

The bounded source audit completed on **2026-09-24** at build 41 source
`8ef8b6df77336950c60506ab2966037f579d92cd`: 19 peers plus the two targets,
13 cross-cutting areas, and 88 exact-SHA source path/line references. It established
**no new actionable defect**. The cleanup concern matched existing 5572011;
recurrence was unproven in that audit. The later owner-reported local confirmation
is recorded above, separately from repaired-package acceptance. This was static inspection, not test
execution, installed-package verification, or live-service acceptance.

The complete peer inventory includes executable extensions and dependency-only
packs:

| Peer group | Every inventoried extension |
| --- | --- |
| Foundry-related executable peers | `azure.ai.agents`, `azure.ai.connections`, `azure.ai.finetune`, `azure.ai.inspector`, `azure.ai.models`, `azure.ai.projects`, `azure.ai.rle`, `azure.ai.routines`, `azure.ai.skills`, `azure.ai.toolboxes`, `azure.ai.training` |
| Other executable peers | `azure.appservice`, `azure.coding-agent`, `microsoft.azd.ai.builder`, `microsoft.azd.concurx`, `microsoft.azd.demo`, `microsoft.azd.extensions` |
| Dependency-only packs | `azd.internal.pack`, `microsoft.foundry` |

Source evidence shows applicable authentication wiring, telemetry/privacy gates,
flag checks, non-interactive/JSON handling, injected writers, cancellation,
pagination, redaction, configuration, and packaging safeguards in the inspected
targets. This is not certification of every command combination. Remaining
installed-boundary coverage includes guest-tenant behavior, opt-out/source
rejection, synthetic credential-bearing diagnostics, and final-candidate
cancellation/state preservation. Explicit per-request time budgets and shared
helper consolidation are optional recommendations, not new release requirements.

Maintain one bounded, evidence-linked inventory of **all** repository peer
extensions at a named source SHA. Compare applicable requirements from the
[extension style guide][extension-style], not unrelated domain features.
For each area, record both extensions' implementation/test evidence, relevant
peer examples, applicability, defect/recommendation distinction, and a regression:

| Area | Required questions |
| --- | --- |
| Authentication | Correct user-access tenant for credentials, native identity flow, explicit endpoint precedence? |
| Telemetry/privacy | Existing telemetry recognized, opt-out respected, classification/redaction proven, no raw customer content? |
| CLI/UX | Explicit flags honored or rejected, deterministic no-prompt/JSON, output kept on the injected writer, current help? |
| Errors/lifecycle | Actionable structured errors, cancellation exits, bounded retries/timeouts, partial recovery and cleanup? |
| Data/configuration | Complete pagination, empty-data behavior, immutable version identity, nested paths and schema migration? |
| Distribution/diagnostics | Core compatibility, package provenance, safe diagnostics, install/upgrade docs and actual-byte tests? |

Telemetry is already included in build 41; an older or separate PR tree must not
be used to report it missing. Compare separate source ancestries carefully.
Record high-confidence correctness defects in ADO after scoped deduplication;
track optional parity recommendations separately. The audit and any proposed
coverage are `NOT RUN` tests until executed. Do not hold a release for unrelated
optional recommendations.

[recipes]: https://github.com/m7md7sien/azd-foundry-feed/blob/6e33b893f1ba4e5f96c84a55f9cad21a26efdf32/Bugbash-Instructions.md
[release-index]: https://github.com/m7md7sien/azd-foundry-feed/blob/main/README.md
[build40]: https://github.com/m7md7sien/azd-foundry-feed/blob/6e33b893f1ba4e5f96c84a55f9cad21a26efdf32/Build-40-Verification.md
[build41]: https://github.com/m7md7sien/azd-foundry-feed/blob/6e33b893f1ba4e5f96c84a55f9cad21a26efdf32/Build-41-Verification.md
[closure40]: https://github.com/m7md7sien/azd-foundry-feed/blob/cd28caf02ec54c5354d2157f75d3602b88fb622b/Build-40-Verification.md
[hosted41]: https://github.com/m7md7sien/azure-dev/actions/runs/35851410814
[hosted42]: https://github.com/m7md7sien/azure-dev/actions/runs/35949654046
[release42]: https://github.com/m7md7sien/azd-foundry-feed/releases/tag/extensions-2026-09-24-42
[release43]: https://github.com/m7md7sien/azd-foundry-feed/releases/tag/extensions-2026-09-24-43
[hosted43]: https://github.com/m7md7sien/azure-dev/actions/runs/35969288265
[feed42]: https://github.com/m7md7sien/azd-foundry-feed/tree/967b631b97cad2d05ca165a985d5010d51bc1f64
[feed43]: https://github.com/m7md7sien/azd-foundry-feed/tree/0f49946b48dab8d2d09c01f380d7e1d3e2c05e07
[scenario41]: https://github.com/m7md7sien/azure-dev/actions/runs/35950594406
[scenario42]: https://github.com/m7md7sien/azure-dev/actions/runs/35951992378
[candidate-repair42]: https://github.com/m7md7sien/azure-dev/actions/runs/35951992376
[scenario-guide]: evaluation-scenario-ci.md
[source42]: https://github.com/m7md7sien/azure-dev/commit/d40a3b5a1e7c5944b1b43decd14c96096a99e5b6
[source43]: https://github.com/m7md7sien/azure-dev/commit/2d4ea18150b06cd6ecc7a31998069f712bf874f9
[source43-required]: https://github.com/m7md7sien/azure-dev/commit/067b2fd5622494db2dc6ac3a2e9bc19e871ed9e7
[init-source-repairs]: https://github.com/m7md7sien/azure-dev/commit/32ff9bc772e7500b37ba99709401709e9bda1165
[agent-rules]: ../../cli/azd/AGENTS.md#testing-best-practices
[extension-style]: ../../cli/azd/docs/extensions/extensions-style-guide.md
[bug-channel]: https://aka.ms/evalsbug
[bug-5640927]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5640927
[bug-5631330]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5631330
[bug-5595070]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5595070
[bug-5595119]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5595119
[bug-5571322]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5571322
[bug-5572139]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5572139
[bug-5530209]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5530209
[bug-5572011]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5572011
[bug-5631310]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5631310
[bug-5645048]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645048
[bug-5645047]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645047
[bug-5571358]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5571358
[bug-5645220]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645220
[bug-5645284]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645284
[bug-5645184]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645184
[bug-5645136]: https://dev.azure.com/msdata/Vienna/_workitems/edit/5645136
