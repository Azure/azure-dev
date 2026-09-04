# Release History

## 1.0.0-beta.1 (Unreleased)

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
  was interrupted, rather than paying for it again.
- `dataset --version` names the version to publish, on `create` and `update`
  alike, and means the same thing as `version:` in the configuration.
- Testing criteria are shaped from each evaluator's published contract, so
  evaluators needing inputs beyond the agent shape -- `ground_truth`, `context`,
  `instruction_id_list` -- work by binding them to dataset columns. A required
  column the dataset does not carry is reported before the request is sent, and
  the message names the column.
- A rubric kept in its own file is referenced at the field it fills:

  ```yaml
  evaluators:
    - name: quality
      definition:
        $ref: ./evaluators/quality.json
  ```

- Results are reported per criterion and per item: pass, fail, skip and error
  counts, a scored pass rate, and the reason a row needs a look. `--status` and
  `--failed-only` narrow the view, and one predicate decides both the table and
  `-o json` so the two cannot disagree.
- Every listing is read a page at a time rather than walking the whole history:
  `--limit` bounds a page, `--after` continues from the last one, and `--all`
  asks for everything deliberately.
- `run output export` writes the run and its items as a single JSON document.
- Reconciliation state is kept in the extension's own private store rather than
  written into the azd environment, so `azd env get-values` shows only what the
  author put there.
