# Installed evaluation scenario CI

This fork-validation workflow exercises the published evaluation and dataset
extensions through the actual `azd` host. It is separate from the immutable
[candidate gate](../../.github/workflows/eval-candidate-proof.yml): it does not
modify that gate's 160 checks, fixture contract, source pins, or four jobs.

## Coverage and limits

| Surface | Implemented coverage |
|---|---|
| Installed packages | Exact core version, extension runtime version JSON, archive checksums, extracted and installed binary digests |
| Practical offline authoring | Existing 160 actual commands on Linux and Windows, including static/turn/simulation authoring, custom config paths, dataset binding, JSON and non-interactive refusal |
| Configuration reproducibility | Six extra actual core commands prove separate `AZD_CONFIG_DIR` profiles do not overwrite or inherit each other's state |
| Cancellation arguments | Two extra actual commands reject multiple run IDs and unknown flags without state mutation |
| Interactive cancellation | **NOT RUN**. Argument refusal and closed stdin do not prove Ctrl+C or explicit Cancel behavior |
| Service-backed creation, deployment, evaluation, export | **BLOCKED / NOT RUN**. No approved existing CI identity, resource tuple, and operation budget are configured |
| Azure DevOps execution | **BLOCKED** until an explicitly authorized organization, project, repository connection, pipeline, and hosted capacity are provided |

A successful **offline** run is not a live Azure quality gate. The manually
requested `live` mode deliberately exits 3 and saves a blocked receipt. It
does not silently skip to success or accept a caller-supplied boolean as spend
authorization. A future live executor needs separately approved existing
identity/resource/budget wiring and bounded cleanup before activation.
This contribution does not implement or claim an authenticated service run.
It creates no identities, grants, runners, infrastructure, or paid generation.

The current live path is **status reporting only**: `live_status()` builds a
blocked receipt and the `live` subcommand writes it, then exits 3. The GitHub
`live-prerequisites` and Azure DevOps `LivePrerequisites` jobs invoke only that
subcommand. No executable agent-create/deploy, dataset-create, evaluation-create,
run, or export service-command wiring is implemented. The receipt records
`executorImplemented: false`; the listed operations describe remaining scope,
not coverage or an executable workflow awaiting a switch.

## Resolve Latest once, then freeze

[The shared runner](../../eng/scripts/eval-scenario-ci/scenario.py) reads the
feed's public GitHub `releases/latest` endpoint once in a producer job. It
records the release ID, immutable tag, publication/resolution timestamps,
source commit, versions and metadata digests. It cross-checks GitHub asset
digests, `SHA256SUMS`, registry entries and publisher provenance before writing
`candidate.json`. Core 1.33.0 and its reviewed archive hashes remain pinned.
A changed core requirement fails explicitly rather than silently upgrading.

Both OS jobs consume the same frozen manifest artifact. They never query Latest.
The original publisher's source declaration is retained as provenance, not
inferred from a filename. Each job verifies archive bytes, computes extracted
and installed executable digests, and checks actual version output. A changed
future CLI contract fails the retained baseline; it is not silently waived.
The baseline command IDs are checked against the ordered, unique
[`checks.json`](../../eng/scripts/eval-candidate-proof/checks.json) contract,
not merely counted.

The subprocess environment is allowlisted, with fresh home, Azure and azd
configuration directories. User tokens, caches, GitHub tokens and pipeline
access tokens are not inherited by CLI subprocesses. After installation the
offline fixture uses a dead HTTP(S) proxy with loopback allowed for host gRPC.
This is not represented as a general-purpose network sandbox.

## GitHub Actions

[Workflow](../../.github/workflows/eval-scenario-ci.yml):

```powershell
gh workflow run eval-scenario-ci.yml --repo m7md7sien/azure-dev `
  --ref m7md7sien-evaluation-github-actions-proof -f mode=offline
```

The dedicated branch's scenario-file pushes also run it. A release publisher
can invoke the same dispatch after publishing/promoting a new package. The
`azd-bugbash-published` repository event is declared for future adoption on the
fork's default branch; GitHub does not deliver it to a workflow present only on
this validation branch.
**Automatic release-event delivery is not activated or proven by this YAML.**
There is no idle release watcher or scheduled repeat-run loop.

Publication continuation currently uses a **native handoff**, not a GitHub
release trigger. The publisher sends the verified public Latest tag, source,
registry digest and provenance to the sole CI owner. The owner deduplicates by
that immutable identity plus the authorized quality round, reuses an already
queued/completed matching run, and dispatches only when a meaningful new round
is needed. The Latest42 handoff was covered by
[run 35951992378](https://github.com/m7md7sien/azure-dev/actions/runs/35951992378)
at `bca776eecd1f3d697b524d3d2ed321999425aae7`, which resolved public 42 once;
the later handoff did not create a second dispatch. This operational receipt
does not establish an automatic webhook or schedule.

## Azure DevOps

[Pipeline](../../eng/pipelines/eval-scenario-ci.yml) uses the same resolver and
runner, a single frozen manifest, and Linux/Windows Microsoft-hosted images.
It has no PR/continuous trigger and no service connection or secret variables.
Register/queue it only in a user-authorized target with existing approved
repository access and capacity. The documented `azure-sdk/internal` pipeline
location is not an execution grant, and this standalone YAML is not a request
to use its shared pools or bypass its production pipeline policies.

No Azure DevOps run URL can be reported until such a target is supplied and
an actual run finishes. Local YAML checks are not an Azure DevOps execution.

## Local reproduction

Use a new output directory for every attempt:

```powershell
python -m unittest discover -s eng\scripts\eval-scenario-ci -p "test_*.py" -v
python eng\scripts\eval-scenario-ci\scenario.py resolve --output evidence\candidate.json
python eng\scripts\eval-scenario-ci\scenario.py offline `
  --manifest evidence\candidate.json --output evidence\windows
```

The runner never edits the checked-in candidate manifest. Existing output or
manifest paths are refused to avoid overwriting evidence.

## Evidence

Each offline artifact includes `candidate.json`, `commands.json`,
`results.json`, `summary.md`, `live-status.json`, synthetic authored YAML and
strict state/binding receipts. `results.json` records provider/run identity,
the shared manifest digest, baseline/additional check counts, exact source,
runtime versions, archive/installed byte comparisons, and cleanup status.
The overall receipt is not marked `PASS` until owned temporary cleanup finishes;
Windows sharing violations receive bounded filesystem retries, not ignored errors.
Command receipts include UTC `startedAt`/`finishedAt`, monotonic
`durationSeconds`, the actual `timeoutSeconds`, and partial sanitized output
when a subprocess times out. Current candidate and scenario receipts use
`PASS`/`FAIL`; archived earlier candidate receipts retain their original
lowercase status values and are not rewritten.
GitHub artifacts
are retained for 14 days; preserve approved immutable receipts in the release
ledger rather than assuming artifacts last forever.

URLs in captured CLI output lose user information, query and fragment credentials.
Only explicitly selected synthetic evidence is uploaded, never home folders,
auth caches, environment dumps, or downloaded credential material.
Resolver consistency checks are not a cryptographic publisher signature.
