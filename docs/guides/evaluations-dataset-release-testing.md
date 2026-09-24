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

Each of the two ongoing testers owns a separate `AZD_CONFIG_DIR`, scenario
directory, artifact/coverage ledger, and resource prefix. Never mutate the normal
global registry/configuration or another tester's agents, runs, files, or sessions.
Follow the [repository testing rules][agent-rules] for `NO_COLOR=1`,
`AZD_FORCE_TTY=false`, and `AZURE_DEV_COLLECT_TELEMETRY=no`. Set these variables in
the same process environment as the tested commands.

The practical tester covers realistic user journeys. The edge-case tester covers
negative inputs, recovery, automation contracts, and confusing UX. Each maintains
its own native 30-minute session automation, with immediate bounded cycles allowed
when a package-ready message arrives. Verify the saved schedule by reading it back.
A configured schedule proves neither that a scheduled invocation occurred nor
that any test passed.

At each cycle, resolve Latest afresh, persist its immutable identity, compare
previous artifact/coverage, and select one bounded useful scenario or regression.
If there is no new artifact or uncovered useful case, return idle. Do not poll
other inboxes, run an infinite loop, create duplicate workers, or repeatedly run
costly cloud cases against unchanged bytes. Pause the cadence when requested or
when execution becomes unsafe.

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

**Current build 42 scope decision:** prioritize a smaller package of independently
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

For new defects, first deduplicate in the relevant project against these records
and known linked fixes. File through the authorized [evaluation bug channel][bug-channel]
with impact-based severity, exact identity, reproducible commands, expected/actual
behavior, redacted evidence, and ownership boundaries. Confusing or misleading UX
is a valid finding even without a crash. Do not assign arbitrary people. If filing
is unavailable, retain a filing-ready receipt marked blocked rather than inventing
a bug ID. Route it to the fixes owner immediately and independently verify the
repaired **installed package** before recommending closure.

## Release evidence index

Snapshot: **2026-09-24**. These links describe their named immutable release only.
The current fresh Windows checks do not replace the historical broader matrices.

| Release | Identity and receipt | Status and limitations |
| --- | --- | --- |
| 40 | [Build 40 receipt][build40], evaluations `1.0.40-beta`, dataset `1.0.0-beta.28` | Historical scoped local/terminal/live results, including the responses-backed execution `FAIL`. Do not relabel as 41. |
| 41 | [Frozen build 41 receipt][build41], evaluations `1.0.41-beta`, dataset `1.0.0-beta.29`, source `8ef8b6df77336950c60506ab2966037f579d92cd`, azd `>=1.33.0` | Published Latest. Historical targeted package acceptance and four hosted jobs `PASS`; newly installed Windows identity/help checks also `PASS`. Broad live reruns, all platforms, and all seven regressions are not claimed. |
| 42 candidate | Integration owner reports frozen source [`d40a3b5a1e7c5944b1b43decd14c96096a99e5b6`][source42], a direct build 41 child with the picker correction, isolated SDK initialization fix, and test-assertion alignment only. Exact package tuple/receipt not yet supplied. | `NOT RUN` for new package acceptance. Preserve 160 baseline CLI checks per OS and target included fixes. Live/new-contract expansion is deferred by explicit user decision, not an active 42 gate. Source checks are not package proof. |

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
| CI-01 | Resolve Latest once, freeze a job manifest with exact release/source/versions/registry/archive/executable digests, and install those bytes in an isolated configuration. Parallel jobs use that same resolved identity, not a moving Latest URL. | `NOT RUN` for the new scenario pipelines |
| CI-02 | Execute actual no-prompt CLI authoring and recovery cases, checking commands, JSON, exit codes, errors, cancellation semantics, reproducible environment/configuration, and state preservation. Record limits of non-terminal cancellation checks. | `NOT RUN` for the new scenario pipelines |
| CI-03 | Publish sanitized command/assertion reports and artifact manifests with actual workflow/build/job links. Download and inspect the artifacts independently; successful YAML validation alone is not a CI execution result. | `NOT RUN` |
| CI-04 | Run the shared offline scenario suite through the new GitHub Actions entry point with least-privilege permissions and bounded timeouts. Keep cloud-service scenarios separate. | `NOT RUN` |
| CI-05 | Run the same offline suite through Azure DevOps using an explicitly authorized existing organization/project/pipeline/repository connection and available capacity. Do not guess or mutate a shared pipeline to manufacture a run. | `BLOCKED` until that execution tuple is verified; YAML/local validation may proceed |
| CI-06 | Wire real create/evaluate/run/export jobs behind explicit identity, existing owned resource, budget, duration, and cleanup parameters. Missing prerequisites produce a clear `BLOCKED` or `NOT RUN` report, never a live-test success. | `BLOCKED`; no approved live identity/resource/cost tuple |

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
| BLIND-05 | Persist coverage and use a bounded 60-minute native cadence for meaningful package/docs changes or a specifically uncovered local case. | Read back schedule configuration; report actual scheduled invocations separately. Return idle when there is no useful new case. |

The two existing package testers retain their 30-minute cadences. The coordinator
may use a 45-minute native check-in to advance actionable pipeline/finding work,
without broker inbox polling, duplicate workers, busy loops, or repeated paid
attempts. While the user is away, choose safe local work and queue missing
approvals instead of asking new questions. Publication authorization does not
authorize cloud testing or remove exact-package acceptance requirements.

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
[source42]: https://github.com/m7md7sien/azure-dev/commit/d40a3b5a1e7c5944b1b43decd14c96096a99e5b6
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
