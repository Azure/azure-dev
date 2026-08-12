# Release History

## 1.0.2-beta (Unreleased)

### Features Added

- Initial release of the Foundry evaluations extension, `azd ai eval`.
- `init` scaffolds `evals/eval_generate.yaml` and `evals/azure.yaml` next to an
  agent, making no service calls.
- `generate` synthesizes a rubric and dataset from the agent's context, writes
  them under `evals/`, and merges `source:` references into the deployment spec
  while preserving comments, ordering and neighboring entries.
- `run` creates the eval group when it does not exist, starts a run, and
  summarizes the result.
- `azure.ai.eval` service-target provider deploys datasets, evaluators and eval
  groups during `azd up`, reconciling them in dependency order.
- Change detection so a repeated `azd up` publishes no redundant versions:
  datasets are fingerprinted locally, evaluator definitions are compared on the
  keys the author wrote, and eval groups are recreated only when their own
  declaration changes.
- Atomic commands for every operation: `dataset`, `evaluator`, `run` and
  `results` subcommands, all supporting `-o json` and `--no-prompt`.
- Testing criteria are shaped from each evaluator's published contract, so
  evaluators requiring inputs beyond the agent shape — `ground_truth`,
  `context`, `instruction_id_list` — work by binding them to dataset columns.
  A required column the dataset does not carry is reported before the request
  is sent, naming the column.

### Bugs Fixed

- `init`, and the errors that name a deploy command, now name the one this
  project can actually run. Eval assets are data-plane only, so `azd up`
  fails compiling a missing `infra/main.bicep` in a project that ships no
  infrastructure; `azd deploy` is reported there, and `azd up` where the
  project does provision.
- A failed eval listing is no longer reported as an eval that was never
  deployed. The two were indistinguishable, so a token or service failure
  told the reader to run `azd up` and publish a second copy of an eval that
  already existed.
- Listings follow their continuation cursors, so an eval, run, dataset or
  evaluator past the first page is no longer invisible. A truncated built-in
  evaluator listing was also silently disabling local validation of an
  evaluator's required initialization parameters.
- A run whose result carries no verdict is no longer counted as a failure.
- Flags and values that were accepted and then ignored are now refused.
