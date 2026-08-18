# Release History

## 1.0.8-beta (Unreleased)

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
  `results` subcommands, all supporting `-o json` and `--no-prompt`.
- Testing criteria are shaped from each evaluator's published contract, so
  evaluators requiring inputs beyond the agent shape — `ground_truth`,
  `context`, `instruction_id_list` — work by binding them to dataset columns.
  A required column the dataset does not carry is reported before the request
  is sent, naming the column.
