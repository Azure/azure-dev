# Release History

## 1.0.18-beta (2026-08-21)

### Bugs Fixed

- An evaluator carrying its rubric under `definition:`, rather than naming a
  file, is now published. Both publish loops selected on `source:` alone, and
  because a written-out rubric leaves `source:` empty they skipped it silently:
  the eval was created bound to an evaluator the service had never been told
  about. The two loops now share one test for what this configuration owns.
- `generate` no longer corrupts a catalog entry it cannot rewrite in place. An
  entry reached through `$ref`, one already carrying its rubric under
  `definition:`, and one pinned to a registered `version:` are each refused with
  an explanation, instead of being written back holding two declarations of the
  same rubric and failing on the next read.
- A `$ref` on one entry no longer changes what another entry means. The rescue
  that moves a spliced rubric under `definition:` was switched on for the whole
  file, so a directive on an unrelated dataset turned a mistyped `dimensions:`
  into rubric content and published it. It is now decided per entry.
- The project directory is read once during a deploy. A second read could fail
  where the first succeeded, leaving artifact paths resolved against the
  extension's own working directory.
- An include reached without a project directory is refused rather than
  silently discarded.

### Other Changes

- `$ref` is modelled on datasets and evals as well as evaluators, so a
  configuration that deploys can also be opened by the commands that edit it.
- Schema descriptions for `file:` and `source:` now say that a path is resolved
  against the evaluation configuration even when the entry arrived through a
  `$ref`, and point at `definition:` for an entry kept in its own file.

## 1.0.17-beta (2026-08-20)

First release of the Foundry evaluations extension.

### Features Added

- Initial release of the Foundry evaluations extension, `azd ai eval`.
- `init` scaffolds `evals/azure.eval.yaml` next to an agent and adds the service
  entry that `$ref`s it to `azure.yaml`, making no service calls.
- `generate` synthesizes a rubric and dataset from the agent's context, writes
  them under `evals/`, and merges `source:` references into the deployment spec
  while preserving comments, ordering and neighboring entries.
- `run` creates the eval group when it does not exist, starts a run, and
  summarizes the result.
- `azure.ai.eval` service-target provider deploys datasets, evaluators and eval
  groups during a deploy, reconciling them in dependency order.
- Change detection so a repeated deploy publishes no redundant versions:
  datasets are fingerprinted locally, evaluator definitions are compared on the
  keys the author wrote, and eval groups are recreated only when their own
  declaration changes.
- Atomic commands for every operation: `dataset`, `evaluator`, `run` and
  `run output` subcommands, all supporting `-o json` and `--no-prompt`.
- `dataset --version` names the version to publish, on `create` and `update`
  alike, and means the same thing as `version:` in the configuration.
- Testing criteria are shaped from each evaluator's published contract, so
  evaluators requiring inputs beyond the agent shape — `ground_truth`,
  `context`, `instruction_id_list` — work by binding them to dataset columns.
  A required column the dataset does not carry is reported before the request
  is sent, naming the column.
