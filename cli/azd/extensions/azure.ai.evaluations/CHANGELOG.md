# Release History

## 1.0.28-beta (2026-08-31)

### Bugs Fixed

- A dataset inside the eval directory is no longer reached with a `..`. Paths
  were rebased against the location, which is the directory before anything is
  written and the configuration file once it exists -- so a second `init` wrote
  `../datasets/rows.jsonl` and the deploy looked for the rows beside the
  project. Reported as 5558481.
- `init` refuses an eval that would differ from one already declared only by
  name, instead of writing it and leaving a deploy to reject the whole file.
  The entry used to stay behind, was offered by `run start`, and then reported
  itself as declared but never deployed. Reported as 5558468.
- A row where every evaluator passed shows why. The reason was only captured
  for evaluators that failed, so a clean run printed an empty REASON column,
  and the evaluators column now says `all passed` rather than a bare dash.
  Reported as 5558666.

### Other Changes

- `eval list` returns a page at a time, 50 rows by default, and prints the
  token for the next one. It walked every page, which on a shared project
  meant seconds of waiting to read your own evals out of hundreds. `--name`
  filters by substring, and the filter is applied before either view renders
  so `-o json` and the table cannot disagree.
- `run start` says it is waiting, and names `--no-wait`. The wait stays the
  default, because `--fail-on` needs the verdict.
- Every run-scoped command takes `--run`, which is the flag the fallback line
  now names.
- A command missing an argument names it, taken from its own usage line,
  rather than reporting `accepts 1 arg(s), received 0`.

## 1.0.27-beta (2026-08-26)

### Bugs Fixed

- `run cancel` could cancel a run it was never asked about. It settles on the
  run you name, or the one this environment started, and no longer falls back
  to the newest run the service lists -- on a shared project that is somebody
  else's, and the listing is unordered anyway. With neither available it says
  so instead of picking one.
- A remembered run that could not be read is reported rather than quietly
  swapped for a different one. Any failure used to fall through to the newest
  run, so a 403, a 500 or a timeout moved the command onto another run without
  saying so. A run that is genuinely gone still falls through.

## 1.0.26-beta (2026-08-26)

### Breaking Changes

- A rubric kept in its own file is now referenced at the field it fills:

  ```yaml
  evaluators:
    - name: quality
      definition:
        $ref: ./evaluators/quality.json
  ```

  Written beside `name:` instead, the file's `{type, dimensions}` landed on
  the declaration and were moved under `definition:` afterwards. That was
  this extension teaching the shared resolver a rule only it knew, and it is
  what made one entry's meaning depend on another's. The old spelling is now
  reported, and the message names the fix.

### Other Changes

- The commands that read, modify and save the configuration read the document
  rather than decoding it, so `$ref` is no longer modelled on six types
  purely to survive a strict decode. Strict decoding now only ever sees a
  resolved configuration, and a `$ref` that reaches it is reported as the
  bypass it is rather than accepted.

## 1.0.25-beta (2026-08-26)

### Bugs Fixed

- The inline example wrote a rubric dimension as `name:`; the service field
  is `id`, so anyone who copied it got an empty dimension ID and a deploy
  that failed. The examples are now checked.
- An absolute `--path` produced `.//tmp/evals/azure.eval.yaml` as the
  service reference, so `init` wrote the configuration where it was asked
  and `azd up` resolved a different file under the project.
- `--evaluator` no longer accepts a path. The value becomes
  `./evaluators/<ref>.json`, so one carrying separators scaffolded a source
  outside the project that deploy would then read and upload.

## 1.0.24-beta (2026-08-24)

### Bugs Fixed

- A second `init` in a project that already has a configuration works again.
  It read back the path the first `init` recorded, which is the configuration
  file, and tried to create a directory of that name -- so it failed with
  `mkdir evals/azure.eval.yaml` before doing anything. Four functions
  created the directory that way; they now share one helper that resolves
  the location first, and a test fails the build if a fifth appears.
- An eval pinned to an explicit `id:` is reserved before any other
  declaration can adopt it. It was claimed only once its own declaration was
  reached, so a declaration listed above it could reach the same eval first
  and the two ended up sharing one run history.
- The container listing no longer reports a partial answer as success. A
  repeated blob marker, or exhausting the page cap, returned the names
  gathered so far -- and those names are what the `.jsonl` is picked from, so
  a container whose data file sat on an unread page yielded the wrong file or
  none at all.
- Answering "no" to a delete now exits 0. It came back as an ordinary error, so
  a reader who deliberately declined got the same exit code as one whose delete
  broke, and anything scripted read that as a failure.
- A page walk that cannot finish now fails instead of returning the pages that
  arrived. A repeated `nextLink`, or one past the page cap, produced a short
  listing indistinguishable from a complete one -- and those rows choose the
  latest version and decide whether a name is ambiguous, so the answer was
  wrong rather than merely short.
- A configuration lock that cannot be taken now fails the command. It warned
  and continued unlocked, which is exactly the lost update the lock exists to
  prevent: two `generate`s both report success and the later write drops the
  earlier one's entry.
- `dataset delete`, `evaluator delete`, `eval delete` and `run delete` now ask
  before removing anything, and take `--force` to skip the question. They
  removed the data immediately, while `azd ai dataset delete` in the companion
  extension asked first: the same operation behaved two ways, and the
  difference only showed when someone typed the wrong name. `job delete` still
  does not ask, because it discards a record of finished work rather than the
  artifact the job produced.
- A 404 raised part-way through a paged listing is no longer read as "no such
  evaluator". The first page had already answered, so the break was the
  continuation failing; the reconciler took it as nothing being published,
  published over a rubric its owner had not changed, and reported success.
- `azd up` now accepts the `.jsonl` files `dataset create` accepts. PowerShell
  writes a byte order mark ahead of the first row, and only one of the two
  paths skipped it, so a file that uploaded cleanly was later refused with
  "row 1 is not valid JSON" pointing at a row that was fine.
- `init --max-traces <n>` no longer fails in a project wired for traces. The
  flag was checked against `--source` as typed, before the default was chosen,
  so it was refused a line before the eval was going to read traces anyway.
- A catalog entry that cannot be rewritten -- one behind a `$ref`, one holding
  its rubric inline, or one pinned to a version -- is now refused before the
  generation job runs. It was refused after the job had been billed and the
  file written.
- `dataset show` reported dataset-client failures through the evaluation
  client's error classifier, so a missing version could surface as an
  unhelpful transport error instead of a named one.

### Other Changes

- The drift message now names `evaluator show --version --output-file`, so a
  reader told their evaluator changed underneath them has the command that
  shows them what changed.
- `azd ai eval run` is described as a command group in the extension manifest,
  and the README quickstart says `azd ai eval run start`. Both read as though
  `run` started an evaluation itself; it prints help.

## 1.0.19-beta (2026-08-24)

### Bugs Fixed

- `generate` and `init` now edit the configuration instead of rewriting it.
  They read it into memory, changed a field and wrote the whole thing back,
  which deleted every comment in the file and changed its indentation. Only the
  entries they add are written now; everything else comes back as it was.
- A `$ref` on an eval's `source:`, on its `target:`, or on an item of its
  `evaluators:` list is now accepted by every command. It deployed, then failed
  the moment `generate` or `init` read the same file.
- A deploy that cannot determine the project directory now fails with an
  explanation. It continued, resolving every relative path against whatever
  directory the command was started from, which could publish a same-named
  dataset from the wrong place.
- A listing the service cannot finish now reports an error instead of returning
  the rows gathered so far. A truncated catalog was indistinguishable from a
  complete one, and these rows decide whether a name is ambiguous.

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
