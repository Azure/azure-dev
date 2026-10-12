# Release History

## 1.0.0-beta.2 (2026-10-12)

### Breaking Changes

- [[#10341]](https://github.com/Azure/azure-dev/pull/10341) Use camelCase keys in `azure.eval.yaml`, for example `evaluationLevel`, `maxSamples` and `dataMapping`, to stay consistent with the other extensions, and reject the former snake_case spellings.

### Features Added

- [[#10116]](https://github.com/Azure/azure-dev/pull/10116) Run conversation simulations: turn scenario seeds into generated conversations and score them.
- [[#10334]](https://github.com/Azure/azure-dev/pull/10334) Add static and simulation conversation authoring to `azd ai eval init`, with separate simulation and judge models, seed validation, and interactive correction of invalid input.
- [[#10334]](https://github.com/Azure/azure-dev/pull/10334) Name new evaluations after their source and mode before falling back to a numeric suffix, and propose a Unicode-safe alternative when a name collides.
- [[#10148]](https://github.com/Azure/azure-dev/pull/10148) Validate datasets, evaluators, version pins and bindings before anything is published, and report what was recovered when a deploy stops partway.
- [[#10311]](https://github.com/Azure/azure-dev/pull/10311) Add `source.type: local` to evaluate an explicit local dataset file without publishing a dataset, validating every row before a `--max-samples` cap applies.

### Bugs Fixed

- [[#10158]](https://github.com/Azure/azure-dev/pull/10158) Fix stored-response evaluations sending incorrect payloads, and migrate previously stored evaluation definitions forward instead of recreating them.
- [[#10113]](https://github.com/Azure/azure-dev/pull/10113) Fix `init` accepting local data it cannot turn into evaluation rows, a published evaluator version losing its display name, categories and supported levels, and closing the eval picker printing "Cancelled" followed by an unrelated ambiguity error and a non-zero exit.
- [[#10116]](https://github.com/Azure/azure-dev/pull/10116) Fix registered datasets being copied inline and the agent being invoked with an empty question during runs.
- [[#10147]](https://github.com/Azure/azure-dev/pull/10147) Fix simulation results presenting requested seed, repetition and turn settings as observed counts; counts the service did not report now read `not reported`.
- [[#10151]](https://github.com/Azure/azure-dev/pull/10151) Fix `azd ai eval dataset download --output-file` rejecting a single uploaded file when its credential is scoped to the parent container.
- [[#10311]](https://github.com/Azure/azure-dev/pull/10311) Fix quality gates and displayed pass rates accepting inconsistent result counts; they now use passed over total test cases and refuse a verdict when the counts contradict each other.
- [[#10146]](https://github.com/Azure/azure-dev/pull/10146) Fix same-named evaluations in different configuration files overwriting each other's IDs and run histories, and keep registered dataset identity and version pins instead of falling back to inline rows.
- [[#10338]](https://github.com/Azure/azure-dev/pull/10338) Fix failed runs and refused requests printing only "failed": show the underlying reason, including nested details and the model they name.

### Other Changes

- [[#10343]](https://github.com/Azure/azure-dev/pull/10343) Clarify what `--max-samples` and `--dataset` accept, and reject an explicitly empty `--dataset=` override.
- [[#10277]](https://github.com/Azure/azure-dev/pull/10277) Explain in the dataset help how `azd ai eval dataset` relates to `azd ai dataset`, and include the `azd` command prefix in help paths.
- [[#10102]](https://github.com/Azure/azure-dev/pull/10102) Report an `init.completed` usage event when installed from the official registry; it carries no names, paths or endpoints.

## 1.0.0-beta.1 (2026-09-17)

First release of the Foundry evaluations extension.

### Features Added

- `azd ai eval` defines and runs Foundry evaluations from the terminal.
- `init` scaffolds `evals/azure.eval.yaml` next to an agent and adds the service
  entry that `$ref`s it into `azure.yaml`. It makes no service calls.
- `generate` synthesizes a rubric and a dataset from the agent's context, writes
  them under `evals/`, and merges the references into the deployment spec while
  preserving comments, ordering and the entries beside them.
- `run` creates the eval group when it does not exist, starts a run, and
  summarizes the result. `--fail-on` turns a quality threshold into an exit
  code, and `--no-wait` returns as soon as the run is accepted.
- An `azure.ai.eval` service target deploys datasets, evaluators and eval groups
  during a deploy, reconciling them in dependency order.
- Change detection, so a repeated deploy publishes nothing redundant: datasets
  are fingerprinted locally, evaluator definitions are compared on the keys the
  author wrote, and eval groups are recreated only when their own declaration
  changes.
- Atomic commands for every operation -- `dataset`, `evaluator`, `eval`, `run`,
  `run output` and `job` -- each supporting `-o json` and `--no-prompt`.
- `job` reattaches to a generation started with `--no-wait`, or one whose client
  was interrupted, rather than paying for it again. `job show` completes it:
  once the job has succeeded it downloads the artifact, writes it under `evals/`
  or the `--output-dir` given, and adds the catalog entry, so a generation and a
  reattached generation leave the same state behind. It is safe to run
  repeatedly, and reports a job still running without writing anything.
- `dataset --version` names the version to publish, on `create` and `update`
  alike, and means the same thing as `version:` in the configuration.
- Testing criteria are shaped from each evaluator's published contract, so
  evaluators needing inputs beyond the agent shape -- `ground_truth`, `context`,
  `instruction_id_list` -- work by binding them to dataset columns. A required
  column the dataset does not carry is reported before the request is sent, and
  the message names the column.
- A rubric kept in its own file is named by `source:`, which is what `generate`
  writes. A `$ref` at `definition:` works too, and is what to use when the
  rubric is written out in the configuration:

  ```yaml
  evaluators:
    - name: quality
      source: ./evaluators/quality.json
  ```

- Results are reported per evaluator and per test case: pass, fail, skip and
  error counts, a scored pass rate, and, in `run output show`, the reason
  behind every result. `--status` and `--failed-only` narrow the view, and one
  predicate decides both the table and `-o json` so the two cannot disagree.
- Eval, run, run-output and generation-job listings are read a page at a time
  rather than walking the whole history: `--limit` bounds a page, `--after`
  continues from the cursor the previous page returned, and `--all` asks for
  everything deliberately. Dataset and evaluator listings answer with an opaque
  continuation link rather than a resumable cursor, so those are fetched in full
  so a name can be found wherever it sits, and there `--limit` bounds what is
  shown and the total says how much was behind it.
- `run output export` writes the run and its items as a single JSON document.
- Reconciliation state is kept in the extension's own private store rather than
  written into the azd environment, so `azd env get-values` shows only what the
  author put there.
