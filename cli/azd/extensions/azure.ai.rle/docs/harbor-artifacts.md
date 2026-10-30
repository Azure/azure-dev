# Harbor task artifact manifests

A task manifest maps complete Harbor task archives to ACR images. It is independent
of the RLE environment name, version, and Foundry project. Reuse it when publishing
another environment version instead of rebuilding the same task images.

## Supply your own images

Build and push your images using your preferred tools. Supply only these fields:

```json
{
  "schema_version": 1,
  "tasks": [
    {
      "task_name": "my-task",
      "local_bundle_path": "tasks/my-task.tar.gz",
      "acr_image_path": "myregistry.azurecr.io/harbor/my-task:v1"
    }
  ]
}
```

Archive paths are relative to the manifest file; absolute paths also work.
Use a tagged or `@sha256:`-pinned ACR reference, not an untagged repository.
Different tasks can reference different registries. The CLI verifies images and
records their digests, but does not rebuild or push supplied images.

Verify and annotate the manifest without contacting RLE:

```sh
azd ai rle publish --prepare-only --task-manifest task-artifacts.json
```

## Build from downloaded task archives

Place individual `.tar.gz` task archives in a directory. Each archive must contain
these regular files at its root:

```text
task.toml
instruction.md
environment/Dockerfile
tests/test.sh
```

Other task files, including `solution/`, can remain in the full archive.
Review the entire archive before uploading; exclude secrets and populated `.env`
files, including files outside `environment/`.
Do not wrap each task in another top-level directory. The archive filename without
`.tar.gz` becomes the default task name. Compressed archives are limited to 64 MiB,
expanded file content to 256 MiB, and archives to 10,000 entries. Links, devices,
unsafe paths, GPU, multi-step, and multi-container tasks are rejected.

```sh
export AZURE_CONTAINER_REGISTRY_ENDPOINT=myregistry.azurecr.io
azd ai rle publish --prepare-only --taskset-dir ./tasks \
  --task-manifest task-artifacts.json
```

`--task-manifest` defaults to `task-artifacts.json` when `--taskset-dir` is given.
An existing manifest is merged by task name; tasks already in it are retained.

This flow requires Azure CLI, `az login`, and the subscription containing the
existing build registry selected in Azure CLI. It uses private source uploads and
one-shot ACR Tasks, not local Docker or local filesystem extraction. ACR extracts
the original archive and builds with `environment/` as its Docker context, keeping
instructions, verifier files and solutions outside that context. Images are
content-addressed by environment file paths, modes and contents.

Archive builds always use ACR; `--remote` is not needed. The first task missing an
image is a canary: the CLI uploads its source, submits its build, waits for success,
and verifies its image digest before submitting any other new builds. Canary
failure stops publishing before the remaining builds are submitted. If every
image is supplied or already prepared, the CLI only verifies those images.

After the canary succeeds, the CLI uploads and submits the remaining sources,
recording each run ID without waiting for that build to finish. ACR controls build
queueing and execution. `--concurrency` bounds simultaneous uploads and submission
requests (default 32, range 1-32), not outstanding or executing builds. Build
monitoring is separate and does not occupy upload slots. Identical build contexts
share a run and image. Each submitted run has a 900-second timeout.

Build compute, registry storage and applicable network charges may apply. The
canary catches shared setup failures, not task-specific failures in later builds.

## Optional CLI metadata and recovery

The CLI atomically updates the same manifest as work progresses. All generated
hashes, digest bindings and build recovery fields are nested inside each task's
optional `_cli` object. Users supplying images never need to author `_cli`.
`acr_image_path` is added only after a CLI-managed image has been pushed and
verified. Keep the manifest for restart and reuse; do not edit it while a publisher
holds its adjacent `.lock` file.

Resume using the manifest alone:

```sh
azd ai rle publish --prepare-only --task-manifest task-artifacts.json
```

Known run IDs are monitored without another upload or build submission. A failed
run is reported rather than automatically rebuilt. If submission was interrupted
before the run ID could be recorded, reconcile ACR runs and attach the identified
`_cli.build.run_id` before retrying. Do not clear uncertain state and submit blindly.
After fan-out begins, a submission error stops new uploads/submissions. The CLI
waits for in-flight requests and monitors known submitted builds to preserve their
results. A failed build does not prevent recording other submitted builds' results. Builds already
submitted to ACR, including queued builds, continue and can incur charges. Exiting
the local command does not cancel them.

### Inspect failures and retry only failed builds

Every preparation attempt writes `<task-manifest>.report.json`, including on build
failure. It lists every task's recorded state, run ID, image and last error, plus
counts of ready, failed, pending, unresolved, in-progress and unverified
tasks. The original manifest retains recovery metadata and successful images.
The report is a local snapshot, not a live ACR status query.

Inspect failed runs with `az acr task logs --registry <registry> --run-id <run-id>`.
Missing or inaccessible upstream base images must be resolved before retrying;
repeating a build does not fix image access. Once the cause is resolved:

```sh
azd ai rle publish --prepare-only --retry-failed --task-manifest task-artifacts.json
```

This rechecks failed runs remotely, then resubmits only terminal unsuccessful
builds. Successful images are verified and reused. Previously failed run IDs and
errors remain in `_cli.build_history`. Pending tasks and uncertain submissions
are not submitted by this command. An interrupted retry with a recorded run ID
is resumed by normal preparation, not resubmitted. Use normal preparation to
continue pending work after reviewing the report.

### Manually remove tasks

To publish a subset, save a private copy of the manifest for recovery, then remove
unwanted entries from its `tasks` array. Leave at least one task and preserve
`_cli` metadata on remaining entries. Do not edit a manifest while a publisher
holds its lock. Removing entries does not cancel remote builds or delete images.

Publish the remaining tasks under a new environment version using the manifest
alone:

```sh
azd ai rle publish --task-manifest task-artifacts.json
```

Do not pass `--taskset-dir` after removing rows: it discovers all archives in that
directory and would add the removed tasks again.

All remaining tasks must be ready before registration; other failures still block
publication. Keep canonical datasets unchanged. If training uses name-only JSONL
selectors, create separate filtered copies for **both** train and validation,
preserving each task's original split. For example, using `jq`:

```sh
jq -c --slurpfile manifest task-artifacts.json \
  'select(.name as $name | any($manifest[0].tasks[]; .task_name == $name))' \
  tasks/train.jsonl > tasks/train-selected.jsonl
jq -c --slurpfile manifest task-artifacts.json \
  'select(.name as $name | any($manifest[0].tasks[]; .task_name == $name))' \
  tasks/validation.jsonl > tasks/validation-selected.jsonl
```

Review selected counts, record the exclusions and input hashes, and pass the new
paths to `train --training-file` and `--validation-file`. Publication does not
rewrite selectors or change the sample's advertised full-dataset counts.

Archives and images are revalidated on every invocation. Editing an image reference
invalidates its previous verification. A verifier-only archive change does not
require a new image. Changed build context invalidates a completed CLI-built image
and prepares a new content-addressed target. Changing inputs while a recorded
submission is unresolved is an error. If an unchanged image tag now resolves to a
different digest, pin the intended digest or deliberately remove stale `_cli`
metadata after reconciliation.

## Register an environment

From the environment directory containing `rle.toml` with `[task] format = "harbor"`:

```sh
azd ai rle publish --task-manifest task-artifacts.json
```

Publish verifies/prepares all images, then uploads the original full archives in
parallel, using the same `--concurrency` limit and RLE's existing content-hash
upload/finalize contract. Signed uploads send bytes directly to storage; URL
issuance and finalization still call RLE. Registration waits for every bundle to
finish and preserves manifest task order and digest-pinned images. Any upload or
finalization failure prevents registration; in-flight uploads finish before the
command returns. Bundle reuse is checked in the selected Foundry project, not
assumed from another project's prior upload.

`--prepare-only` does not require `rle.toml` or a Foundry endpoint. Normal
registration still requires Foundry access and image-pull permissions. Preparing
images alone does not prove that the backend accepts a particular full archive
or that its runtime isolates verifier/solution files correctly; verify those
contracts with your backend before running tasks.

Existing `--task-dir` source-directory publishing remains available and cannot be
combined with `--task-manifest` or `--taskset-dir`.
