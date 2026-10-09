# Azure Developer CLI (azd) Evaluations Extension

Define Foundry evaluations alongside your agent in `azure.yaml`, deploy them
with `azd up`, and run them from the terminal.

```bash
azd ai eval init          # scaffold evals/ next to your agent
azd ai eval generate      # synthesize a rubric and dataset from the agent
azd up                    # register datasets and evaluators, create the eval group
azd ai eval run start     # run the evaluation and summarize the results
```

Generation prints an interactive `init` next step, followed by guidance for
unattended use. When using `--no-prompt`, supply an independently selected
`--judge-model <deployment>`; conversation simulation also needs
`--simulation-model <model>` or a `connection-name/model-deployment` reference unless
exactly one previously configured simulator reference is available in the selected
evaluation configuration. Plain names are passed unchanged without a connection
lookup. Qualified references validate the selected Foundry project connection
before authoring files. The printed command never assumes that the
generation model should fill either role.

When no instructions are supplied or detected locally or from the deployed agent,
interactive `generate` offers **Type instructions** or **Load from file**.
File selection reads a non-empty local text file, including paths containing spaces;
enter the path without shell quotes at the prompt. Both routes report the source
and reach the same generation confirmation. The generation context and plan show
only the instruction file's basename, not its parent directories or contents;
the full supplied path is still used to read the file.
An unreadable, missing, directory, or empty file is reported before any job;
the interactive file prompt accepts a corrected path without restarting the
other selections. Ctrl+C cancels without submitting jobs or writing artifacts.
Explicit `--agent-instruction` or `--agent-instruction-file` values take precedence
and skip detection and selection. Empty explicit values are rejected.
Under `--no-prompt` or `--output json`, missing instructions produce an error naming
these flags instead of a prompt. For example:

```bash
azd ai eval generate --agent-instruction-file "./instruction files/agent.txt" --target support-agent --generation-model generation-deployment --no-prompt
```

Generation derives default artifact names from the agent's deployed name in local
project metadata, not from its service key or instruction source. A bare invocation,
`--target` with that key, and `--target` with the deployed name therefore use the same
prefix. A confirmed absence of an azd project preserves an explicitly named
remote agent, so standalone generation still derives defaults. Other project
lookup failures require explicit artifact names or a retry; the CLI does not
silently switch prefixes on a transport or permission failure.
Unsupported Project RPCs and environment-absence errors do not establish that
the project itself is absent and therefore do not enable this fallback.
The naming prefix stays separate from the original target selector, so deriving
artifact names does not introduce an additional deployed-name lookup at submission.
Dataset generation names are limited to 50 characters. Long default prefixes use
a deterministic shortened stem with a hash, shared by turn and conversation
datasets, while retaining `-turn-tests` or `-conversation-tests` and room for a
collision number. Explicit overlong `--dataset-name` values are rejected, never
truncated. This generation-specific dataset limit is not imposed on evaluators
or on existing asset lookups. Existing files and catalog entries are not renamed.
If an explicit dataset name leaves no room for a collision number, the collision
prompt offers only regeneration or cancellation rather than truncating the name
or its evaluation-level suffix.

## What gets deployed

Eval resources are one service entry in `azure.yaml`, normally a `$ref` to a
file under `evals/`:

```yaml
# azure.yaml
services:
  ai-project:
    host: azure.ai.project
  evals:
    host: azure.ai.eval
    uses: [ai-project]
    $ref: ./evals/azure.eval.yaml
```

```yaml
# evals/azure.eval.yaml
datasets:
  - name: support-golden
    file: ./datasets/support-golden.jsonl

evaluators:
  - name: support-quality
    source: ./evaluators/support-quality.json

evals:
  - name: support-quality
    dataset: support-golden
    evaluationLevel: turn
    evaluators:
      - evaluator: builtin.task_adherence
        initializationParameters:
          model: gpt-4.1-nano
      - evaluator: support-quality
    target:
      type: agent
      name: support-agent
```

Keys in the configuration are camelCase, like the rest of `azure.yaml`:
`evaluationLevel`, `maxSamples`, `initializationParameters`, `dataMapping`, the
`source` keys (`lookbackHours`, `maxTraces`, `agentName`, `agentVersion`,
`responseIds`, `maxTurns`, `startTime`, `endTime`), the `simulation` keys
(`numConversations`, `maxTurns`) and an evaluator's catalog metadata
(`displayName`, `supportedEvaluationLevels`). That is the only spelling read: a
snake_case key such as `evaluation_level` is reported as an unknown key, with
the camelCase key suggested. Only the configuration's own keys are camelCase.
What a key holds (an evaluator's `initializationParameters`, a `dataMapping`'s
inputs, a rubric's `definition`) and the JSON the service returns, which `-o json`
prints as the service sent it, keep the service's names.

`azd up` reconciles **datasets → evaluators → eval groups**, in that order,
because a group references the versions the first two resolve to.

Relative paths inside the `$ref`'d configuration resolve against **that file's**
directory, so `./datasets/x.jsonl` above means `evals/datasets/x.jsonl`.

The same holds for a `$ref` on a single catalog entry: `file:` and `source:` are
registered as this extension's path keys, so a relative `source:` written inside
`evals/evaluators/quality.yaml` means `evals/evaluators/quality.json` — beside
the file it was written in, wherever that file is pulled in from.

Properties beside a `$ref` override the referenced definition and use the same
camelCase keys and value types as its inline configuration shape. For example,
`$ref: ./quality.yaml` with `maxSamples: 5` overrides an eval's cap;
`max_samples` is rejected by both the editor schema and the CLI. Required fields
may come from the referenced file and are checked after resolution. Rubric
`definition:` overlays retain the evaluator service's own vocabulary instead.

A rubric kept in its own file is named by `source:`, which is what `generate`
writes:

```yaml
evaluators:
  - name: quality
    source: ./evaluators/quality.json
```

`definition:` takes the rubric itself rather than a path, and a `$ref` there
fills that field with the file's contents:

```yaml
evaluators:
  - name: quality
    definition:
      $ref: ./evaluators/quality.json    # the rubric itself, not a pointer to one
```

An entry declared this way is read and deployed normally, but the rubric lives
in the referenced file, so `azd ai eval generate` will not update it in place and
says so rather than writing a second declaration of the same rubric beside the
directive. Edit the referenced file, or generate under a different name.

### Evaluator data mappings

The CLI sends explicit `data_mapping` entries; it does not rely on server
auto-mapping. Standard datasets and trace sources use the following complete
defaults, regardless of which optional properties the evaluator catalog lists:

| Evaluation level | Default `data_mapping` |
| --- | --- |
| Turn (including an omitted level) | `query: "{{item.query}}"`, `response: "{{item.response}}"`, `tool_calls: "{{item.tool_calls}}"`, `tool_definitions: "{{item.tool_definitions}}"` |
| Conversation | `messages: "{{item.messages}}"`, `tool_definitions: "{{item.tool_definitions}}"` |

Optional tool columns stay in the mapping even when absent from a dataset.
An explicit `source.type: local` instead omits optional default item bindings for
columns not present in every row. Authored mappings and required evaluator inputs
are never dropped; missing columns fail validation.
The CLI does **not** infer `context`, `ground_truth`, or other evaluator-specific
inputs from catalog properties, even when a matching column exists. Map those
inputs explicitly when needed. An additional required evaluator input without
a mapping is refused with instructions to provide real source data.

Generated and retrieved responses have distinct output sources:

| Evaluation input | Default response or interaction binding |
| --- | --- |
| Model target | `query: "{{item.query}}"`, `response: "{{sample.output_text}}"` |
| Agent target or stored response IDs | `response: "{{sample.output_items}}"` for structured responses; `response: "{{sample.output_text}}"` when the evaluator declares a string-only response |
| Simulated conversations | `messages: "{{item.messages}}"` |

Agent-generated tool inputs use `sample.tool_calls` and
`sample.tool_definitions`. A target name used to filter traces does not invoke
the agent: trace mappings always use the completed `item` fields above.

`messages` and separate `query`/`response` mappings are alternative interaction
formats, never combined. To score a messages-only static dataset at turn level,
explicitly set `data_mapping: {messages: "{{item.messages}}"}`. The run's
`evaluation_level` still controls the scoring level.

An explicit mapping overrides the corresponding default without changing the
authored configuration. Mapping `messages` selects conversation-format defaults,
including `tool_definitions`; mapping either turn field selects turn-format
defaults, including `tool_calls` and `tool_definitions`.
Explicitly combining both formats or supplying an empty binding is rejected.
Renamed columns retain the standard evaluator input's type:

```yaml
evaluators:
  - evaluator: builtin.groundedness
    initialization_parameters:
      model: gpt-4.1-nano
    data_mapping:
      query: "{{item.question}}"
      response: "{{item.answer}}"
      context: "{{item.reference_text}}"
```

For groundedness, a stored text response uses supporting `context` from the
dataset through an explicit mapping. Structured responses can instead include tool results, and complete
conversations use `messages`. `query` and `response` accept strings or arrays of
message objects; `tool_definitions` accepts a string, object, or array of objects.
These values are sent without flattening them into strings. If the dataset uses
nonstandard names, map them explicitly as above. A mapping cannot supply context
that the dataset or recorded interaction does not contain.
Before `create` or `azd up` publishes dependencies, required mapped interaction
columns in static datasets must be present on every row and contain a non-empty
string or an array. Empty arrays remain accepted; empty or whitespace-only strings,
numbers, booleans, nulls, and bare objects are rejected. Optional tool columns are
not subject to this interaction-input check.
These client mappings do not guarantee a successful groundedness score. Missing
grounding data or a service-side evaluator error still needs investigation using
the actual input and per-evaluator output.

See the Foundry documentation for
[groundedness inputs](https://learn.microsoft.com/azure/foundry/concepts/evaluation-evaluators/rag-evaluators#using-rag-evaluators),
[target response bindings](https://learn.microsoft.com/azure/foundry/observability/how-to/cloud-evaluation-targets#set-up-evaluators-and-data-mappings),
and [conversation mappings](https://learn.microsoft.com/azure/foundry/observability/how-to/cloud-evaluation-conversations#define-the-data-schema-and-evaluators).

#### Immutable evaluation definitions

Stored evaluation mappings are immutable. Switching between trace and
stored-response source modes creates a
new definition when their stored input contracts differ; changing only filters,
time windows, response IDs, or row caps keeps the existing history.
Known item-versus-generated-output conflicts require a compatible definition for
model and agent targets, even without a `source:` block. Missing stored contract details
do not trigger a speculative replacement. Explicit ID pins are never silently
replaced; known incompatible response mappings are refused.

To deliberately apply the current mappings to an existing managed evaluation:

1. Record its current ID and inspect its mappings with `azd ai eval show <eval-id> -o json`.
2. If the declaration has an explicit `id:`, remove that pin from the declaration
   you intend to update, or create a separate unpinned managed declaration.
   An explicit ID cannot change that existing definition; known incompatible
   response mappings are refused. Retain the recorded ID to inspect its history.
3. Set the intended explicit `data_mapping` entries and change one evaluator
   reference's criterion `name`, for example `name: groundedness_mapped`. This
   changes the immutable definition. Changing only the eval group's name can
   rename or reuse it instead.
4. Run `azd ai eval create <eval-name>` for that unpinned declaration, or `azd up`.
   Confirm the new ID and mappings with `show` before starting another run.

The recorded evaluation and its runs are retained. Use its ID to inspect
that history. These commands define the client request;
it does not establish a successful hosted groundedness result.

### Registered dataset identity

Runs bind registered datasets using the service-issued version ID, including
datasets whose catalog entry still has a local `file:` after publication.
An explicit `version:` takes precedence over the recorded publication version;
without either, the latest registered version is resolved from the service.
Run metadata records that same resolved version.

Registered versions cannot be sampled by this run API. A positive `maxSamples:`
or `--max-samples` is refused rather than ignored or sent as anonymous inline
rows. Remove the cap, or publish and select a smaller dataset.

For an ordinary dataset eval selected by name, an explicit `--max-samples 0`
clears its configured cap. Trace/response sources and reruns selected by a bare
eval ID reject every explicit `--max-samples` value, including zero, rather than
silently ignoring it. Omit the flag to repeat a previous run's source; use
`source.maxTraces` to limit a declared trace source. Simulation declarations
with a positive configured cap remain invalid even when the flag is zero.

Genuinely unregistered local files still run inline and support a cap, but only
after a typed not-found version-list response and not-found first-version probes
confirm absence. A successful empty listing remains indeterminate when those
probes find nothing: later registered versions may exist even if early versions
were deleted. The run fails instead of selecting local data; retry after the
registry catches up or declare a known dataset version. Permissions, transient
failures, and malformed listings also fail the run without a local fallback.

Trace- and response-backed runs reject positive configured `max_samples:` and explicitly supplied
`--max-samples` flags; use `source.max_traces` for trace limits or select
`source.response_ids` explicitly. Reruns selected by eval ID also reject an
explicit `--max-samples`, including zero, because they repeat the previous source.

Reruns repeat the stored registered `file_id`. If the stored source contains inline
rows attributed to a registered dataset, start the declared eval by name instead:
replacing those possibly capped rows with a whole version would change what gets
scored.

The JSON handoff from `run start --no-wait -o json` retains the submitted dataset
name and registered version even when the create response omits that metadata.
Local unregistered runs do not invent a version, and anonymous reruns remain
unattributed.

`job show --dataset` recovers the registered evaluation level even when the local
artifact already exists. It preserves edited bytes unless `--force` is given,
does not download content when preserving the file, and does not record a new
deployed fingerprint for those unverified local bytes. Job inputs and recorded
generation state keep precedence over the registered tag. A metadata lookup
failure is reported as a collection error; an untagged version stays unspecified.
Within registered metadata, an explicit `evaluation_level` wins over a recognized
`data_generation_type`, followed by the portal's `scenario: conversation_simulation`.
Unrecognized metadata tags do not establish an evaluation level.
Echoed generation inputs are used internally to resolve the level and are omitted from
job JSON output, including source prompts and instructions.

### Explicit local files without dataset publication

To deliberately evaluate local bytes, author a separate eval with `source.type: local`.
This is not a fallback for an absent or unreadable registry name:

```yaml
evals:
  - name: quality-local
    source:
      type: local
      file: ./datasets/local-rows.jsonl
    max_samples: 10
    evaluation_level: turn
    evaluators:
      - evaluator: builtin.relevance
        initialization_parameters:
          model: gpt-4.1-nano
```

Use rows containing the fields the evaluator needs, for example:

```jsonl
{"query":"What is the return period?","response":"Returns are accepted within 30 days."}
```

Create the eval explicitly with `azd ai eval create quality-local`, then invoke
`azd ai eval run start --eval quality-local`. Creation validates local input and
creates or reuses the eval without registering a dataset. A run sends the selected
rows as `file_content`; it does not publish a dataset or look up a dataset name.
**This is not offline evaluation:** an explicitly invoked run uses the normal
Foundry service and evaluation billing.

`source.file` is a filesystem path, not a URL. It resolves relative to the
configuration that contains it, including a nested `$ref` declaration. The local
source is exclusive with `dataset`, `simulation`, trace/response fields, and any
explicit `--dataset` flag. `init --dataset <file>` scaffolds a publishable catalog
entry; it does not opt into local-only behavior. Declare the separate local eval
in the configuration.

All rows must be non-empty JSON objects and satisfy the target and evaluator
mappings, even rows beyond a cap. An evaluator reference's explicit version wins
over its catalog entry's version; both select that exact version's contract rather
than latest. A failed pinned lookup never falls back to another version.
A failed evaluator-contract lookup stops local preflight rather than using an
incomplete catalog. Before run submission, the CLI also checks the
registered eval's stored mappings and `item_schema`; an unreadable definition or
unsupported external schema reference fails rather than submitting unchecked
rows. Invalid run input causes no submission or dataset/state mutation.

For `create` and `azd up`, preflight checks every local row against the prospective
authored contracts of custom evaluators this operation will publish, as well as
the selected contracts of already-published evaluators, before dependency writes.
Available authored schemas take precedence over the published service catalog.
**Service-added constraints that are absent from both the authored and existing
published contract cannot be known before publication.** The CLI reads the exact
new evaluator version and checks local rows again before creating the eval. If a
new service-added constraint rejects them then, the evaluator version may already
have been published, but no eval or run is submitted. Preflight does not promise
zero publication for constraints the service has not yet disclosed.

Mapped local columns retain the evaluator's published property constraints,
including numeric, array, object, and nullable types. Constraints from multiple
evaluators consuming the same column all apply; an absent type contract is not
invented as a string type. Explicit evaluator version pins select the same contract
for mapping construction and local type validation. Evaluator publication invalidates earlier catalog
snapshots before subsequent eval creation.
On local-source evals, positive `max_samples` limits submitted rows and
`--max-samples 0` overrides a configured cap. Trace/response caps, registered
dataset pins, and indeterminate registry listings follow the constraints above.

Validation streams the entire file, including rows beyond a cap, while retaining
only the rows a capped run can submit. Create/deploy preflight retains no row set.
Uncapped runs still retain every submitted row. Repeated scans use the same file
handle and reject content changes detected during validation.

A referenced evaluator missing from a successful catalog listing is read directly;
an unreadable or absent referenced contract is not replaced with permissive
defaults. Authored evaluators awaiting publication are validated from their
prospective definition instead.

Changes to immutable local-source criteria or item schema create a new eval
instead of reusing stale mappings, including when an optional standard-mapped
column becomes available in every row. Unmapped extra columns alone do not
change the request. Changing only the row cap does not recreate the eval.
An unpinned criterion echoed by the service as `evaluator_version: latest`
is equivalent to an omitted version; explicit versions and actual contract
changes still require the corresponding immutable eval. When a local eval is
renamed and its old name is reused for a different prepared contract, deployment
preserves the original ID and run history for the rename and creates only the
replacement. Targeted create continues reserving unselected siblings' IDs.

An opaque source `$ref` may resolve to any source type, so its cap is validated
after resolution by the CLI; the editor constrains caps when the source type is
present in the same document.

Local runs carry no registered dataset name/version, fabricated `file_id`, or
source path in request metadata or the JSON handoff. Only normal run-ID bookkeeping
is performed after submission; dataset publication versions/fingerprints are not
changed. A rerun selected by eval ID repeats the stored inline snapshot, not a
fresh read of the file. Run the declared eval by name to use edited bytes.

The JSON start handoff treats dataset attribution as a name/version pair. A
service version without a dataset name cannot replace the submitted pair. A
complete returned pair takes precedence; a returned name alone inherits the
submitted version only when the names match. Without a dataset name from either
source, the handoff omits the version. Raw service metadata is not rewritten.

An explicitly empty `--dataset` value is rejected for every run source and ID
rerun. A configuration cannot declare both `dataset` and `source`, even when
the dataset value is empty.

### Simulating multi-turn conversations

Choose how conversation datasets are used during `init`:

```bash
# Score completed message transcripts, without invoking an agent.
azd ai eval init --conversation-mode static --dataset completed-transcripts --judge-model judge-deployment

# Create conversations from scenario seeds, then grade the resulting messages.
azd ai eval init --conversation-mode simulation --target support-agent --dataset retail-seeds --simulation-model model-connection/simulator-deployment --judge-model judge-deployment --num-conversations 1 --max-turns 5 --no-prompt
```

`--conversation-mode` implies `--source dataset` and
`--evaluation-level conversation` when they are omitted. Without this flag,
interactive init offers **Static** or **Simulation** for a conversation dataset;
`--no-prompt` and `--output json` default to static. Static mode writes neither
`target:` nor `simulation:` and rejects `--target`, because completed transcripts
are scored as they stand. Trace-backed conversations continue to use
`--source traces --evaluation-level conversation` and filter by the selected agent.

Default eval names identify the source, conversation mode where applicable, and
evaluation level, rather than distinguishing different flows only by a number:

| Authoring flow | Default name |
|---|---|
| Turn dataset | `<target>-dataset-turn-eval` |
| Turn traces | `<target>-trace-turn-eval` |
| Conversation traces | `<target>-trace-conversation-eval` |
| Static conversation dataset | `<local-agent>-static-conversation-eval` |
| Simulated conversation dataset | `<target>-simulation-conversation-eval` |

An explicit `--name` still wins. Only a collision with the descriptive name adds
`-2`, `-3`, and so on. Existing eval names and declarations are not renamed.
Static mode uses the sole local agent service only as a naming hint, not an
invocation target. With no single local agent, the fallback is
`static-conversation-eval`; init does not invent an agent identity.

| Init flag | Applies to | Meaning |
|---|---|---|
| `--conversation-mode static\|simulation` | Conversation datasets | Completed messages or scenario-seed simulation. |
| `--simulation-model` | Simulation only | Plain model name or `connection-name/model-deployment` that plays the simulated user; explicit values override previously configured simulator references. |
| `--num-conversations` | Simulation only | Conversations per seed, 1 to 5; default 1. |
| `--max-turns` | Simulation only | Maximum turns, 1 to 20; omission preserves the service default. |

Explicit zero is invalid for both numeric flags. Simulation flags with static,
turn, or trace evaluation are rejected rather than ignored. Simulation needs an
agent target, seed dataset, simulation model, and judge model. Non-interactive
init reports all unresolved required inputs together, naming the flags to supply.
Init is add-only, preserves existing YAML and unknown fields, and makes no new
service writes. Qualified simulation references read the Foundry project connection catalogue before
creating any configuration, directories, locks, or root-service wiring.
When `--simulation-model` is omitted, init can reuse immediate
`evals[].simulation.model` strings from the selected configuration. One distinct
valid reference, plain or qualified, is selected automatically; several offer a picker with
**Enter another name**. Under `--no-prompt` or `--output json`, several bindings
require an explicit `--simulation-model` before any writes. With no usable
binding, interactive init lists eligible Azure OpenAI connections and then asks
for the deployment name; unattended init names the missing flag. Explicit and
reused plain model names do not trigger catalogue lookup or connection inference.
Qualified references must name an existing Azure OpenAI project connection.
Missing, unknown, or wrong-kind connections and failed or partial catalogue
reads stop init without authored writes. Connection discovery follows all pages.
This checks connection identity and kind, not whether a deployment is ready or
whether the target service accepts a particular evaluator request. Init never copies
a judge or generation deployment into the simulator selection, or opens unrelated `$ref` files to find
simulator suggestions, and it never changes existing model selections.

Authored evaluation configuration must contain one YAML document with unique,
literal string top-level keys. Init and catalog edits reject multiple documents,
duplicate keys, and merge, alias or complex top-level keys rather than silently dropping
or ambiguously updating content. Aliases in values remain supported.
Authoring rejects a symbolic link selected as the config file or directory before
creating locks or editing configuration, leaving both the link and its target
unchanged. Trailing separators and normalized `.` components do not bypass this
check. Select the target config file or real directory directly to edit it.
If adding the root project service fails and the host acknowledges that
the operation finished unsuccessfully, init rolls back its eval-config edit so the
same command can be retried after restoring root write access.
The root snapshot uses the host's `azure.yaml`, then `azure.yml` filename
preference. A missing or changed root filename is an uncertain snapshot and
requires retaining the scaffold for inspection.
The initially selected root filename is retained across confirmation; if the
selection changes before writing, init refuses instead of following a new file
that the running host did not select. This includes a root appearing after
initially being absent.
Existing config bytes are restored; only a new config written by that attempt
is removed. Dataset files, artifact directories, lock files, and existing
`.gitignore` rules are retained. If either configuration changes during wiring,
the host's save outcome is uncertain after cancellation or a connection failure,
or rollback fails, init reports that recovery is incomplete and leaves an
explicit inspection instruction rather than overwriting concurrent edits.
Automatic rollback requires the host to return a matching operation acknowledgment.
Without that acknowledgment, even an explicit root-save
or permission error has an uncertain outcome: init retains the scaffold and
reports manual inspection instead of an automatic retry.
Inspect the retained eval and its root service reference;
do not delete preexisting evaluations.
For automatic rollback after rejection before a save, including unsupported
layered projects, the host must also acknowledge that operation's unsuccessful
completion. Completion is not proof that the
root file stayed unchanged; byte comparisons and ownership checks still apply.
For every dataset mode, init checks locally available files for non-empty JSONL
object rows before creating locks, ignore files, artifact directories, or
configuration. Malformed JSON, empty datasets, arrays, scalars, and empty objects
are rejected without those writes. This structural check does not invent required
columns for an evaluator; evaluator-specific contracts are checked separately.
For simulation, init also checks every locally available seed row before writing
configuration, including files in declared datasets and local nested `$ref`
entries. Dataset lookup skips broken unnamed includes when a later unnamed
include matches the requested name; if none matches, the first include error is
reported. Each row needs a non-whitespace text `test_case_description` of at most
2,500 Unicode characters and cannot carry `messages`, `query`, or `response` fields (even empty
or null), and may specify a positive whole
`simulation_configuration.desired_num_turns`. It must not exceed the per-row
`simulation_configuration.max_num_turns`, or otherwise `--max-turns`, or the
service default of 20 when neither is specified. Omitted turn counts remain valid.
Interactive init reports invalid rows and asks for a corrected or different
dataset before confirmation; press Ctrl+C at that prompt to cancel without
authored changes. Under `--no-prompt` or `--output json`, invalid local rows
fail immediately without writing configuration.
When the effective turn cap is too low, init names a valid `--max-turns` value
to use on a new invocation, or asks you to lower the row's desired turns.
Correcting the dataset never silently changes the selected cap.
Local files derive their dataset name from the filename without its extension.
For a new declaration, that name must use only letters, digits, dashes, and
underscores, up to 255 bytes. Reusing an existing authored or registered
dataset keeps the broader lookup-name rules: the name must be non-empty, cannot
be `.` or `..`, and cannot contain path separators or control characters.
If that name is already declared for a different file (or has no local file),
init refuses the collision rather than replacing the declaration or ignoring
the supplied path. Interactive init asks for another dataset; use a unique
filename to add the new file, or the existing dataset's name or file path to
reuse it. Equivalent paths to the same file are accepted, preserving references,
version pins, and other authored metadata.
When `--path` names a configuration file, dataset lookup uses that exact file,
while artifact paths remain relative to its directory.
The configuration destination must not be the selected local dataset itself,
including equivalent paths, hard links, or symbolic links to the same file.
Init rejects that conflict before authoring and rechecks identity before writing;
choose a separate `--path` destination rather than replacing the input rows.
If a directory's selected canonical or legacy configuration filename changes
while init is awaiting confirmation, init refuses the changed destination.
Review the files and retry with an exact `--path` filename. Under the configuration
lock, validation, scaffold writes, and root wiring all use that same filename.
The successful human `eval create` next step retains that filename rather than
selecting the default config in the artifact directory.
If that path cannot be portably quoted, init displays escaped exact-name/path
values and manual create guidance instead of a runnable placeholder command.
New paths ending in `.yaml` or `.yml` are treated as configuration files,
including absolute paths and paths containing spaces. Existing directories
remain directories, even if their names end in `.yaml`.
Init supports `--output default` for human-readable output and `--output json`
for structured output. Unsupported formats are rejected before any authored writes.
Registered datasets with no local file are not fetched or checked by init.
For a nonempty local `supported_evaluation_levels` list, the evaluator picker
requires an exact case-insensitive match for the selected level; an explicit
incompatible `--evaluator` is rejected. Unfamiliar entries do not grant support
for other levels. Missing or empty lists remain unconstrained, with authoritative
compatibility checked when the eval is created.
Omitting `--evaluator` keeps the default selection or opens the interactive
picker. An explicitly empty `--evaluator` is rejected rather than silently
restoring the default.
The picker shortlist is `builtin.task_completion`, `builtin.customer_satisfaction`,
`builtin.coherence`, and `builtin.groundedness`, with only `builtin.task_completion`
selected by default. Composites such as `builtin.output_quality` and
`builtin.tool_use_quality` require explicit `--evaluator` selection; an explicit
selection replaces the default rather than adding evaluator constituents.
When the built-in catalogue is reachable, unattended init validates the whole
default set. Interactive init excludes unavailable recommendations and validates
the final selection, so an unavailable default does not prevent choosing an
available built-in or compatible declared custom evaluator. An unread catalogue
is not treated as an empty successful response.
Local names, scaffolding, and mock tests do not establish production evaluator
availability or accepted IDs and initialization parameters; validate those
against the target Foundry project and API.

The example above grades rows that already hold an exchange. A `simulation:`
block instead has the service hold the conversation first — a simulator model
plays the user against your deployed agent — and grades the transcript it
produces:

```yaml
evals:
  - name: retail-conversations
    dataset: retail-seeds
    evaluationLevel: conversation
    simulation:
      model: model-connection/gpt-4.1-nano # plays the user, not the agent under test
      numConversations: 3      # per seed row, 1–5
      maxTurns: 8              # 1–20; omit to leave it to the service
    evaluators:
      - evaluator: builtin.task_completion
        initializationParameters:
          model: gpt-4.1-nano
    target:
      type: agent
      name: support-agent
```

`simulation:` requires `evaluationLevel: conversation` and an agent target:
there is no turn to score before the conversation exists, and nothing to hold it
with if the target is a model. It is also exclusive with `source:` and positive
`maxSamples:` caps; `maxSamples: 0` means uncapped. The run creates its
conversations rather than collecting or sampling ones that already happened.
Every evaluator listed has to support
conversation level; one that does not is refused at deploy rather than bound to
a column the graded rows do not have.

Omit `numConversations` to use one conversation per seed, and omit `maxTurns`
to use the service default. Explicit zero or null values for either of these
simulation counts are rejected by both file-based and inline service configuration
loaders.

The authored `simulation:` block accepts 1 to 5 conversations per seed and
1 to 20 turns when those defaults are explicitly set. These are azd's current
authoring limits from the CLI feature specification, not maxima imposed by the
Foundry preview service. Per-case settings follow
the override rules below.

`simulation.model` accepts a plain model name or a
`connection-name/model-deployment` reference, without Unicode whitespace or
control characters. Plain names are sent unchanged without guessing a connection
or reusing the judge model. During init, qualified references validate the selected
Azure OpenAI project connection; interactive discovery offers eligible connections.
Local validation does not establish model deployment readiness or service acceptance.

The dataset holds **seeds**, not exchanges. One row describes one conversation
to have:

```jsonl
{"test_case_description": "A customer asks why a delivered order never arrived.", "simulation_configuration": {"desired_num_turns": 4}}
{"test_case_description": "A customer disputes a charge and wants it reversed."}
```

Only `test_case_description` is required; it is the scenario the simulator opens
with and must contain non-whitespace text of 1 to 2,500 Unicode characters. Per-row turn settings belong
inside `simulation_configuration`, matching
the [published Foundry contract](https://github.com/Azure/azure-rest-api-specs/blob/main/specification/ai-foundry/data-plane/Foundry/src/openai/evaluations/user_conversation_simulation.tsp).
The optional `desired_num_turns` must not exceed the effective `max_num_turns`:
the per-row maximum overrides `simulation.maxTurns`, and the service default is
20 when neither is set. When correcting a row that exceeds `simulation.maxTurns`,
keep that authored cap within 1 to 20; if raising it cannot satisfy the row within
those bounds, lower the row's desired turns to fit the current cap.
Generation can return a flat top-level `desired_num_turns`.
When collecting generated conversation seeds, the CLI moves that value into
`simulation_configuration` in the downloaded local file. Canonical rows remain
byte-identical, and unrelated fields are preserved without rounding numeric IDs.
The returned artifact version identifies the original generation job's output,
not the normalized local bytes. The CLI clears stale local publication state
instead of recording a deployed fingerprint for those transformed bytes:
`azd ai eval create` or `azd up` publishes the file explicitly before a simulation run
binds the resulting service-issued version ID. Collection never silently publishes
a replacement version, and an existing edited file is still preserved unless
`--force` is supplied.

An independently registered dataset still carrying a flat turn field is rejected
at run time because the simulator would ignore it. Move the field into
`simulation_configuration` and explicitly publish a new version before running.

Runs send `data_mapping` for `test_case_description` and
`simulation_configuration` as column names, not `{{item...}}` templates. Registered
seed content stays bound by version ID rather than being rewritten inline.
The simulated eval's graded `messages` column is a required array of message
objects, not a string. Ordinary static dataset schemas keep their existing
optional-column behavior.

Local seed files are checked row by row before `create` or `azd up` publishes
dependencies; the run checks registered seed rows as well. Completed `messages`
and turn-level `query`/`response` fields cannot be mixed with simulation seeds,
even when those fields are empty or null. Keep these dataset modes in separate
evaluations.

Seed rows carry no `query` or `response`, because nobody has asked anything yet.
That is why the evaluators bind `messages` — the transcript the run produces —
and why a run over seed rows whose target reads a column the seeds do not have
is refused instead of scored against the seeded text.

`azd ai eval generate --evaluation-level conversation` writes seeds in this
shape and tags the registered dataset so a later run knows what it holds.
Its printed init command selects `--conversation-mode simulation`. Run that
command interactively to enter the simulation model, or add
`--simulation-model <connection-name/model-deployment> --judge-model <deployment> --no-prompt` for
automation (also supply `--target` if generation had no agent).
If generation had no agent and none is declared locally, supply `--target` before
running the command interactively too. Rubric-only generation does not supply a
dataset: select existing data with `--source dataset --dataset <name-or-path>`,
or choose `--source traces` with a configured trace connection. The printed
unattended guidance includes the missing target and dataset flags.
The generation, simulation, and judge deployments are independent choices.
Init never copies the generation or judge model into the simulation model.
Generation declares artifacts only; it does not attach them to an existing eval
or replace its configuration. If a generated rubric declares an incompatible
evaluation level, the handoff warns and leaves `--evaluator` unset. Init offers
its `builtin.task_completion` default, which the user can replace; the incompatible
rubric remains in the catalogue.
If any handoff value contains shell expansion syntax or cannot be portably quoted,
including a dollar sign, backtick, double quote, percent sign, exclamation mark,
backslash, caret, or any Unicode terminal control,
generation displays the exact escaped values and manual initialization guidance
instead of a copyable command. Quote that path for your shell when supplying
`--path`; generation never substitutes a different path into a runnable handoff.

Simulation run summaries, `run show`, and `run output list` retain the run's
dataset name and version and distinguish **requested configuration** from
**observed results**:

- Seed scenarios count the validated dataset rows submitted to the run.
- Repetitions are the requested conversations per seed, not completed conversations.
- Maximum turns is a requested ceiling. An omitted ceiling leaves the service
  default and is not an observed conversation length.
- Conversation evaluation results use the service's `result_counts`, keeping
  failed verdicts separate from errored and skipped evaluations.

The CLI has no verified service counters for generated conversations, completed
conversations, or actual turns. These are shown as **not
reported**, never calculated by multiplying seeds and repetitions or treating
evaluation totals as successful generation. Runs without recorded
settings also show **not reported** for those settings. Static conversation
and turn-level runs report evaluation results.

When a waited `run start` successfully reads all output rows for its mean-score
summary, it also shows **observed conversation output**. This block counts
unique `datasource_item.id` values and the associated output-item lifecycle
statuses, not generated or completed conversations. A completed output item can
still have failed evaluation verdicts. Duplicate conversation IDs count once;
conflicting or unknown statuses and rows without conversation IDs are reported
separately. These observations describe all rows returned by that listing, not
a guarantee that every requested conversation produced output. Paged or filtered
listings, and detail views that have not fetched all rows, do not supply this
block. Only an explicitly reported zero evaluation total skips this best-effort
fetch; missing or null totals do not imply empty output. No additional output
fetch or transcript-based turn inference is used.

JSON retains the service's run fields, including unrecognized nested fields;
missing or null result-count members remain missing or null. It does not add
zero counters to partial `per_testing_criteria_results` entries or a score/verdict
to an output result that omitted those fields. Explicit zero, false, and null
values remain distinct; changed typed values and reported score normalization
are still reflected in JSON. It does not add
estimated conversation or turn counts. Numbers in echoed inline datasets,
including nested source content, retain their exact precision in run JSON.
Simulation runs record configuration under `metadata.azd_simulation_*`, with
`metadata.azd_run_mode` identifying the simulation mode.
Read `run show -o json` for the run object.

### Repeated deploys do not create redundant versions

Before publishing dependencies, `azd ai eval create <name>` validates the selected
eval, its local JSONL/rubric files, and its registered dataset/evaluator references,
including version pins. An unavailable reference lookup is an error, not a reason
to publish optimistically. A valid, complete empty evaluator-version listing
allows the first publication of a local rubric; it does not create an evaluator
for an existing-only reference. An initial evaluator-version listing 404 also
permits first publication; a 404 when reading a specific version does not establish
that the evaluator is absent. Missing or malformed list data and failed
continuation pages remain errors. Unrelated invalid evals do not block this targeted
command; `azd up` validates the entire evaluation service before publishing any
of its dependencies. Validation does not write private reconciliation state.
The target-input check for a local dataset invoking an agent or model requires
`query` in at least one row, using the union of row fields. This is separate from
required evaluator inputs and explicit item bindings, whose columns must occur
in every row. Static dataset-only evaluations do not impose the target-input
requirement. A rubric must supply a `dimensions` array; omission and `null` are
invalid, while an explicit empty array retains its existing meaning. Rubric
dimension weights, when supplied, must be whole numbers
from 1 to 10; `pass_threshold`, when supplied, must be a number from 0 to 1.
Bounds and whole-number checks use the exact authored JSON value, including
decimal and scientific notation, without floating-point rounding. A missing
definition `type` is accepted for a hand-authored rubric; an explicitly null,
empty, or whitespace-only type is invalid. These checks apply before publication and before replacing
downloaded or collected rubric files.

This is not a transaction across Foundry resources. If a later service operation
fails, successfully published shared versions are retained, not deleted. Fix the
reported error and repeat the same command to reuse unchanged artifacts.
For a partial `create -o json` failure, the single output document includes
`status: "failed"`, the resolved `artifacts` with their versions and `published`
flags, the error, and a `recovery_command`. When the error supplies remediation,
`error.suggestion` preserves it alongside `error.message`. The command still exits nonzero.
URLs in both `error.message` and `error.suggestion` omit user information, query strings, and fragments.

Datasets are fingerprinted locally, because the dataset API exposes no content
hash and comparing against the service would mean downloading the blob on every
deploy. Evaluator definitions are compared against the service, but only on the
keys you authored — the service adds `data_schema`, `init_parameters` and
`metrics` of its own.

An evaluator reference inherits an explicit `version` from its catalog entry
unless the reference sets its own version. Changing or removing that inherited
pin changes the immutable eval criteria and creates a new eval. An unchanged
effective pin keeps the same eval, including when the pin moves between the
catalog and reference. With neither pin set, the evaluator continues tracking
the service's latest version without recreating the eval on each new version.
Whole-service deployment rejects identical effective eval definitions, including
when equivalent pins are spelled in different places. Targeted create still
validates only its selected declaration and reserves the other evals' IDs.
Renamed declarations can reuse an eval only when its stored criteria confirm
the same effective pins and no other declared eval owns it. A stored criterion
with a different effective pin requires a separate eval. Matching criterion
identities and pins permit reuse when a recorded fingerprint omits pin information.
Creating a separate eval preserves the original eval and its runs.
Reuse requires complete stored criterion identities and effective pins;
missing or conflicting evidence requires a separate eval.

After a local rubric is reconciled, its evaluator contract is read from that
exact service version rather than a potentially stale discovery listing.
This contract read does not add an authored version pin. An unavailable or
malformed contract is an error, not permission to use a different schema.
Preflight uses the same digest-aware reuse decision: when the rubric will not
be republished, its existing service contract wins over authored metadata
overrides. A genuine edit that will publish a new version keeps authored
metadata precedence.
If that prospective publication would overwrite an externally advanced evaluator,
preflight reports the drift before publishing any dataset. Reconciliation checks
again before evaluator publication to catch changes that occur after preflight.

Registered dataset references are checked against the JSONL rows of the settled
version before publication. This uses the existing read-credential/content
path and requires permission to read those rows; unavailable or malformed
content is not treated as an unknown schema that accepts every binding.
Required evaluator columns must be present in every row. Reconciliation keeps
the inspected version even if a newer version appears during the command.
Primary interaction bindings in the prepared criteria are also checked:
mapped `messages`, or mapped `query` and `response`, must resolve to columns
present in every dataset row. Optional tool columns are not made required by
this check, and generated sample bindings and simulation outputs are not
mistaken for input dataset columns.
An explicitly authored `dataMapping` is stricter than an optional default:
each item column it names must exist in the known source rows, including explicit
tool, context, and ground-truth bindings. Simulation default inference remains
`messages`-only. An explicit `tool_definitions` binding opts into a generated field
rather than a seed column; it does not expand inferred defaults or guarantee that
a service response supplies that optional field.
When an unchanged local dataset file is repinned to another registered version,
preflight reads that selected version's content. The original file-to-published-
version baseline is retained; denied metadata or content reads stop reconciliation.
For unchanged, unpinned local datasets, reuse requires a successful point read
of the recorded version, including when the version listing is empty or delayed.
A confirmed missing version is republished from the local file, retaining the
recorded version as the publication floor. Denied or failed reads stop
reconciliation before publication. Explicit pins are not repaired or moved:
a missing pinned version is an error. Preflight-selected versions are checked
again before reuse.
The point read is required even when the listing reports the recorded version
or only older versions.

Eval groups are immutable, so a change to a group's evaluators, target,
evaluation level, or source type creates a new group and a new id. Per-run sampling,
response IDs, and trace filters retain the same ID while the stored contract remains compatible.
The id is cached in the extension's own private state (`eval.state`) so repeat
runs stay comparable. That is not an azd environment value: it does not appear
in `azd env get-values`, which shows only what you put there.

Stored-response evaluations (`source.type: responses`) use Foundry's
`azure_ai_source` schema with `scenario: responses`. Human `azd ai eval show <eval>`
output displays `Data Source` and `Scenario` for non-custom definitions so a
response eval can be distinguished from a custom-schema eval. JSON output
retains the complete `data_source_config`.
A deployment creates a compatible eval when a stored response eval uses a custom
schema. The original eval and its runs are retained; unchanged compatible
deployments reuse the same ID. Other evaluation modes retain compatible
custom schemas and their runs. Trace declarations also accept
existing SDK-created `azure_ai_source` definitions with `scenario: traces` or
`traces_preview`; declarations do not assume unknown schema types are compatible. Switching a declaration from stored
responses to another source also creates an eval with the required custom schema.

Stored-response turn evaluations bind retrieved output through the sample
namespace without invoking a target. A published evaluator contract that requires
a string uses `{{sample.output_text}}`; structured or unspecified response types
retain `{{sample.output_items}}`. Conversation mappings retain `{{item.messages}}`,
and explicit `dataMapping` values take precedence.

Managed response evals with conflicting item/sample bindings or incompatible
text/items response bindings require a compatible eval, retaining the original
eval and its runs. Missing inferred mappings and unrelated service enrichment do not
require replacement. An explicit `id:` with conflicting response or trace
source contracts is refused before dependency publication; remove the `id:` and deploy the
declaration to create a compatible eval. Each explicitly authored `dataMapping` field must match the
stored criterion exactly, including the column name, not just the item/sample namespace.
Missing authored bindings or a missing/renamed stored criterion for those bindings
are also conflicts; inferred defaults retain the narrower source-compatibility checks.
These checks apply to both built-in and custom evaluator references.

A trace target names an agent filter, not a new invocation. Managed trace evals
with positively identified conflicting sample bindings or custom sample-schema
settings require completed-item bindings, including when the agent filter
is declared with `target.name`. The original eval and its runs are retained. Explicit
`dataMapping` values still win, and compatible SDK trace scenarios ignore unrelated
sample-schema enrichment. Missing or unknown evidence does not require replacement;
unrelated optional/default mapping changes require a deliberate criterion change.

An explicit `id:` or a rerun by eval ID cannot change an immutable eval's
schema. An incompatible response eval fails before starting a run. Remove the
explicit `id:`, deploy the response-source declaration, then run it by name.
Rerun sources with bare response-ID rows are also rejected; running the
declaration by name builds the required `item` envelopes without invoking an
agent or changing the selected response IDs. Stored-response runs reject
`--max-samples` (including explicit zero) and configured row caps; select
`source.responseIds` to control which stored responses are evaluated.
Inline reruns must map `response_id` to `{{item.<field>}}`, with a non-blank
string ID at that field in every item. Invalid reruns identify the zero-based item
index and reason without printing stored response IDs. Response-source IDs must not be blank.
The current [Foundry deployed-interaction evaluation guidance](https://github.com/MicrosoftDocs/azure-ai-docs/blob/9d5bbd1edaf03beacdf92af2b0dcaacbff82d895/articles/foundry/observability/how-to/cloud-evaluation-deployed-interactions.md#evaluate-interactions-by-response-id)
supports only `file_content` response retrieval on the OpenAI v1 evaluation-run
route. Although the SDK model union includes `file_id`, the service documents that
it returns HTTP 400. Such reruns are rejected locally; the CLI never silently
downloads or expands a selected file into response IDs.
The run checks schema compatibility in both directions, including a trace source
switch or `--dataset` override of a response eval, before submitting a run.
Bare-ID reruns retain other source/schema pairs from their previous run unless
there is a known response/trace scenario mismatch. The schema read is required:
an unreadable definition does not establish compatibility.
Editor validation and create/deploy preflight reject positive `maxSamples`
for trace- and response-source declarations, including sources loaded through
`$ref`. Explicit local sources support positive `maxSamples` caps; zero means
uncapped.

### Recovering partial generation

Dataset and evaluator generation are independent. If one fails, a successful
artifact remains registered, downloaded, and declared in the catalog. A failed
catalog update is reported separately from a failed generation or download;
it does not discard the downloaded artifact.
Recollecting an existing evaluator artifact without `--force` preserves its
authored catalog metadata, including explicit empty values, while filling
missing metadata from the job. `--force` replaces the artifact and refreshes
those catalog fields, including explicitly empty category and evaluation-level
lists. An omitted list does not clear an existing field.

Use the printed `azd ai eval job show <job-id> --dataset` or `--evaluator`
command to inspect or collect the existing job without starting another one.
The recovery command preserves the configuration path, output directory,
project endpoint, and environment. If submission returned no job ID, inspect
the printed `job list` command first: a lost response does not prove that the
service never accepted the job. If a new generation is needed, repeat the
original command with **only the failed artifact selector**, keeping that
artifact's original input flags. Do not regenerate the successful artifact.

With `-o json`, generation emits one document keyed by `dataset` and `evaluator`,
including each outcome's `status`, `job_id`, and, on failure, `error`,
`recovery_command`, and `retry_guidance`. An optional `suggestion` preserves
structured remediation while keeping `error` a string. A failed outcome also
carries the same stable `code` every other command's failure reports. Status is
`submitted`, `succeeded`, `failed`, or `catalog_failed`. Any failed outcome
makes the command exit nonzero.
URLs in both `error` and `suggestion` omit user information, query strings, and fragments.

Generation changes catalog declarations, not an existing eval's references.
When an existing eval does not reference a generated artifact, the command
explains that it remains declaration-only. Use `init` to create a new eval or
deliberately edit a compatible eval's references. A trace-backed eval cannot
also consume a dataset; keep it unchanged and create a separate dataset-backed
eval instead.

## Commands

### Dataset identity and row caps

Runs over registered datasets send the service-issued version ID, not inline
copies of the rows. This applies to static scoring, agent and model targets,
and datasets whose declaration still has `file:` after publication. A declared
`version:` wins over the version recorded by deployment; otherwise the recorded
version is used, or the latest service version when none is recorded. Lookup,
authorization, and missing-ID errors stop the run rather than switching to inline
data. Registered rows are downloaded only to validate their shape before submission.

The current run API exposes no supported row-subset option on a registered
`file_id` source. A positive `--max-samples` or `max_samples:` therefore fails
explicitly for registered datasets. Remove the cap, pass `--max-samples 0` to
override a configured cap, or deliberately publish and select a smaller dataset.
The CLI does not publish temporary subset datasets automatically.

Unregistered local files support inline rows and row caps only after a typed
not-found version listing and not-found first-version probes confirm absence.
A successful empty listing is indeterminate and does not permit inline fallback.
Malformed listings, incomplete pagination, and authorization or service failures
stop the run.
`--max-samples` is also rejected for trace- and response-backed runs
and reruns selected by eval ID, where it cannot change the repeated source.
Trace- and response-backed runs reject positive configured `max_samples:` too; use `source.max_traces`
for trace limits or select `source.response_ids` explicitly.

Reruns repeat the stored registered `file_id`. If the stored source contains inline
rows attributed to a registered dataset, start the declared eval by name instead:
replacing those possibly capped rows with a whole version would change what gets
scored.

| Group | Commands |
|---|---|
| `azd ai eval` | `init` · `generate` · `create [name]` · `list` · `show <eval>` · `delete <eval>` |
| `azd ai eval dataset` | `create` · `update` · `list` · `show` · `download` · `delete` · `versions list` |
| `azd ai eval evaluator` | `create` · `update` · `list` · `show` · `download` · `delete` · `versions list` |
| `azd ai eval run` | `start` · `list` · `show` · `cancel` · `delete` · `output list` · `output show` · `output export` |
| `azd ai eval job` | `list` · `show` · `cancel` · `delete` |

`create` and `update` both publish a new immutable version; the server
auto-increments and nothing mutates in place.

`delete` asks before removing anything and takes `--force` to skip the
question, which is what a pipeline passes. `job delete` is the exception: it
discards a record of finished work, not the artifact the job produced.

After a successful eval deletion, the command removes its local named aliases,
fingerprints, and scoped identity references, including when given a service ID.
Unrelated eval scopes and shared dataset/evaluator versions are preserved.
Failed or ambiguous deletes do not clear state. If local cleanup fails after
the service has deleted the eval, a warning reports that failure separately.

Every command supports `-o json` and `--no-prompt`, so the whole surface is
usable from CI.

Known-command flag-validation failures also return one JSON error document
under `-o json`, including mutually exclusive flags such as `--wait --no-wait`
and missing required flag groups. They still fail without running command hooks
or writing evaluation artifacts.

Every command's `-o json` failure document carries a stable `error.code`
alongside `error.message` (and `error.suggestion` when one applies); `generate`'s
own partial-result document (below) carries the same `code` per failed outcome
instead. A local validation failure (an invalid `--evaluation-level`,
`--evaluator`, `--fail-on`, or `--max-samples`, or a malformed local dataset row)
reports its own code. Cobra's own Args and flag-group failures, and any other
error this CLI does not classify, report a stable unclassified code rather than
omitting the field or guessing one from the message text. A refused request
from the backing service (for example a missing run or a conflicting delete)
keeps its diagnostic sentence and, when the service supplied one, its own code;
the JSON document omits the full internal service endpoint that sentence would
otherwise carry, while the human-readable and stderr diagnostics still name
which service
answered.

`azd ai eval run output list --failed-only` displays a page of failing test
cases, not the full run's failure count. Its footer separates the number shown
on that page from the service-reported failures and total test cases for the
whole run once it reaches a terminal state. While a run is still moving, partial
counters are not labeled as full-run totals and export guidance describes the
available results, not a completed run. For example, a page of 10 failures can
belong to a run with 12 failures among 18 test cases. Follow the printed page token to read the rest,
or use `run output export` to save the complete results. Errored rows remain
separate from failed verdicts and can be selected with `--status errored`.
The same outcome filters apply to paged output, `--all`, and `--output-file`,
in both human and JSON modes. `--failed-only` adds failed cases to any outcomes
selected by `--status`; `--status failed` alone has the same page-count footer.
An empty selection is reported as no matching results, not as a run with no
scored rows. `--all` summaries omit the page-only qualifier.

After a terminal run, waited `run start` summaries and `run show` details
include an unfiltered output-list command and a JSON export command, both with
the resolved eval and run identities. Suggested commands use the immutable eval
ID when known, rather than a friendly name that might point to a different
eval after a later deployment. Friendly labels remain in the human run header;
service JSON is not rewritten. A failed-only listing is additional
guidance when the service reports failed verdicts, not a replacement for the
unfiltered listing. Errored rows get a separate `--status errored` command;
they are not included by `--failed-only`.

For responses without a status, reported counters provide useful
available-result guidance, including all-passed and explicit-zero counts,
without asserting that the run has completed. If a run lookup omits its `id`,
follow-up requests retain the explicit or remembered lookup ID separately;
JSON and exports preserve the original service fields.

Human summaries, run details, and listings also distinguish unreported counters
from explicit zeros. Partial counters are marked `not reported` rather than
inventing a failure/error split or a pass rate without known operands.
Waited summaries also show a complete set of explicitly reported zero counters;
their pass rate is `-` because the total is zero.
Displayed run pass rates and `--fail-on pass-rate=<0..1>` use `passed / total`
test cases. Failed, errored, skipped, and otherwise unaccounted rows count
against the run. Per-evaluator criterion rates use only rows with a passed or
failed verdict.

Both pass-rate and `any-failure` gates require reported `total` and `passed`
counts; neither requires a reported `failed` count. Missing or null required
counters make the gate indeterminate: the command returns an operational error
(extension exit 1), never a quality verdict based on invented zeros.
A reported zero total breaches either gate when `passed` is absent or zero.
Inconsistent counts outside `0 <= passed <= total` make either gate indeterminate
and display the run pass rate as `not reported`, without changing service JSON.
For a pass-rate gate, when all outcome counts are reported but their sum leaves
rows unaccounted for, a neutral warning names that gap without assigning failed,
errored, or skipped outcomes. The pass-rate denominator still includes all reported test cases;
explicit zero error/skip counts are preserved. Determinate quality breaches
retain extension exit 2; the azd host exposes extension
failures as exit 1.

An operationally failed run can have no result counts or output rows. Its
follow-up commands inspect **available** output and export the run's diagnostics
plus any available results; they do not imply that grading succeeded or that
failing rows exist. `run show` also prints the service's run-level failure
message when one was returned, removing URL credentials, query strings, and
fragments from the human message. `--output json` keeps its existing document
shape and exit behavior without appending human guidance.
Human portal/report links also remove URL credentials, query strings, and
fragments before display, without rewriting the underlying service fields.
Each link is validated as a whole URL. Malformed, ambiguous, or raw
whitespace/control-bearing values display as `<redacted-url>` rather than
exposing any part of the rejected value.
Sanitized links are labeled as redacted and may open a general portal page
when query-based routing was removed. Use the resolved output-list/detail
commands to inspect the exact run; routing parameters are not exempted from redaction.

CLI-generated JSON error envelopes, accompanying stderr diagnostics, and the
run's known `error.code`/`error.message` fields in JSON and exports also redact
embedded URL credentials, including malformed HTTP(S) URLs concatenated to
identifiers without a separator. A value separated from an unfinished URL query
assignment by whitespace is redacted with that malformed URL candidate, rather
than copied into the remaining diagnostic prose. This projection does not mutate the service response
or rewrite dataset/output content and unknown fields. Those other fields can
still contain sensitive source data; keep exported files private.

`run output show <item>` uses the lookup ID from the listing in its human
header. The service may return a result-version URI as the detail object's
`id`; JSON keeps that returned identity rather than replacing it with the
lookup ID.

For rubric results, the detail view displays returned
`properties.dimension_scores` alongside the overall evaluator score. Each
dimension can include its score, applicability, weight, and full reason.
Applicability is not a pass/fail verdict, and missing values are not treated
as zero or false. Separate dimension metrics in `results` remain supported.
The CLI does not derive dimension results from the rubric definition when
they are absent from the response.
Malformed dimension properties are identified by a warning without hiding valid
aggregate results or other evaluators. The human command still returns an error;
`--output json` retains the original data for inspection.
Each `dimension_scores` entry must be an object; null or scalar entries are
malformed data, not unnamed dimensions with unreported values.

Output-item JSON preserves unrecognized nested service fields, including
evaluator `properties` and `sample` details; modeled scores keep their existing
numeric normalization. Numeric dataset values retain their precision rather
than being rounded through floating-point decoding. These fields can contain
prompts, answers, and other sensitive evaluation content. Prefer a private destination with
`run output list --output-file` or `run output export --output-file` over
writing JSON into shared terminal or CI logs.

A command that needs an eval and was not told which one offers a picker.
Selecting **Cancel** is an answer, not a failure: the command says the selection
was cancelled and exits 0, at every command that offers it. Pressing Ctrl+C
interrupts the prompt and exits nonzero, without reporting a successful
cancellation. Under `--no-prompt` or `-o json`, no picker is shown; an ambiguous
eval still produces an error, and no cancellation prose is written to stdout.

`azd ai eval create` closes with a Portal link for both created and reused evals.

### Downloading a dataset

`azd ai eval dataset download <name> --version <version> --output-file <path>`
supports single-file datasets even when their download credentials grant access
to the parent container. Container-backed downloads require a complete listing
with exactly one file and dataset metadata reporting `isSingleFile: true`.
Folders (including one-file folders) and multi-file datasets require
`--output-dir` and retain their relative layout.

Single-file downloads without `--output-file` land as
`<name>-<version><extension>` under `--output-dir` (the current directory by
default), while folders land under `<name>-<version>/`. Omitting `--version`
selects the latest version. Existing destinations require `--force` to replace,
including with `--no-prompt`. JSON output reports the resolved version, path,
file count, and single-file status.

Overwrite protection also applies to destinations created while a download is
in progress. A cancelled transfer leaves existing content unchanged and removes
its temporary download files, even with `--force`.

## Evaluators

Built-ins need no declaration — reference them as `builtin.<name>` and list
them with `azd ai eval evaluator list --builtin`.

`init` offers `builtin.task_completion`, `builtin.customer_satisfaction`,
`builtin.coherence`, and `builtin.groundedness` in its picker, with only
`builtin.task_completion` preselected or used under `--no-prompt`. This is a
shortlist, not the catalogue. Any built-in the project publishes works with
`--evaluator builtin.<name>`, including ones the picker never shows. Composites
such as `builtin.output_quality` and `builtin.tool_use_quality` require explicit
selection; they are not init defaults. Explicit selections replace the default,
and existing evaluator references, parameters, and mappings remain unchanged.

`init` checks that reference against the project's catalogue when it can reach
one, so a name that does not exist is refused there rather than at `create`.
When no project is reachable — offline, unauthenticated, or outside an azd
environment — the reference is preserved without catalogue validation.

Evaluators do not share an input contract, so the CLI reads each one's
published contract and shapes the request to match. An evaluator needing an
input your dataset does not carry is reported before the request is sent, with
the column named, rather than as a service-side rejection.

A custom rubric is a JSON list of weighted dimensions:

```json
{
  "dimensions": [
    { "id": "accuracy", "description": "The answer is factually correct.", "weight": 5 },
    { "id": "tone", "description": "The answer is polite and professional.", "weight": 2 }
  ]
}
```

`weight` is an **integer from 1 to 10**. Weights do not need to sum to
anything.

### Validating explicit local rows

Primary mapped `query`, `response`, or `messages` values follow the same rules as
declared datasets: empty or whitespace-only strings and non-string/non-array
values are rejected, even when the evaluator schema permits them. Empty arrays
remain accepted. The entire file is checked, including rows beyond a configured
cap, using both authored and stored mappings before publishing dependencies or
submitting a run.

### Editing a registered rubric

Download a version, edit its dimensions or pass threshold, then publish the edit:

```bash
azd ai eval evaluator download support-quality --version 3 --output-file ./support-quality.json
azd ai eval evaluator update support-quality --from-file ./support-quality.json
```

A rubric download uses the same editable JSON shape as generation and job
collection: `type: "rubric"`, `dimensions`, and `pass_threshold` when supplied.
Each dimension retains `id`, `description`, `weight`, and `always_applicable`.
Unknown definition and dimension fields are preserved for future authoring
contracts, including their numeric precision. Only known service-envelope and
catalog fields, service metadata (`metadata`, creation details, generation
details, and warnings), and generated wiring (`data_schema`, `init_parameters`,
`metrics`, and `prompt_text`, including camel-case aliases) are omitted.
Prompt-based evaluators retain their
separate full document, including their authored prompt. To inspect or export the full service response,
use `azd ai eval evaluator show support-quality --version 3 -o json`.
Malformed recognized rubrics fail download and collection before replacing an
artifact or updating its catalog entry, rather than falling back to a full
service-envelope export.
Download and generation results must be JSON objects: null, arrays, strings,
numbers, and booleans are rejected before writing files or catalog entries.
Unknown object-shaped evaluator documents remain supported without dropping fields.
`create` and `azd up` also reject null or non-array `dimensions`, non-object
dimension entries, and wrong-typed `id`, `description`, or `always_applicable`
values during preflight, before uploading or tagging datasets or publishing
evaluators. Validation preserves authored bytes for digest and drift decisions.

Standalone `evaluator update` preserves the existing display name, description,
categories, and supported evaluation levels. A full input document can explicitly
replace those fields. The download does not modify configuration or attach the
evaluator to an eval. When using the downloaded rubric as a declaration's
`source`, `create` and `azd up` preserve missing catalog fields from the current
published version. Explicit fields in a full input document take precedence
over catalog declarations, which take precedence over inherited service
metadata. Explicit empty category and evaluation-level lists clear those
fields. Metadata joins the publication body after digest and reuse decisions,
so an unchanged rubric remains unpublished.
Definition comparisons preserve exact JSON numeric values, including unknown
dimension fields: adjacent large integers and precise decimal edits are changes,
while equivalent spellings such as `1`, `1.0`, and `1e0` compare equal even before
a local publication fingerprint has been recorded.

Omitting `--version` downloads the latest version and reports which one was used.
Existing files are not replaced unless `--force` is supplied.

## Choosing a project

The project endpoint is resolved in this order:

1. `--project-endpoint`
2. `FOUNDRY_PROJECT_ENDPOINT` in the active azd environment, then
   `AZURE_AI_PROJECT_ENDPOINT` there
3. `extensions.ai-projects.context.endpoint` in azd's global config, which
   `azd ai project` writes and this extension only reads. If that key is absent,
   the fallback is `extensions.ai-agents.project.context.endpoint`.
4. `FOUNDRY_PROJECT_ENDPOINT` in the host environment, then
   `AZURE_AI_PROJECT_ENDPOINT`

Level 3 is worth knowing about: it is machine-wide rather than per-project, so
a project selected with `azd ai project` somewhere else takes precedence over
the variable exported in this shell. `--debug` prints which level answered.

## Other environment variables

| Variable | Description |
| --- | --- |
| `AZURE_AI_PROJECT_ID` | The Microsoft Foundry project resource ID, used to build portal links for an eval and its runs. |
| `AZURE_AI_MODEL_DEPLOYMENT_NAME` | The model deployment `azd ai eval init` offers as the judge when one is not named on the command line. |
| `APPLICATIONINSIGHTS_CONNECTION_STRING` | A detection signal, not a credential this extension consumes: `azd ai eval init` and `generate` check only whether it is set, and default to a trace-backed source when it is. The value is never read or transmitted by the extension — Foundry reads the traces server-side. |

## Local development

### Prerequisites

- Go (the version in `go.mod`; `GOTOOLCHAIN=auto` fetches it)
- [azd](https://aka.ms/azd) and the extension developer kit:
  `azd ext install microsoft.azd.extensions`

### Build, test, install

```bash
azd x build          # compile and install into the local azd
azd x pack           # package the artifacts
azd x publish        # register in the local extension source
azd ext install azure.ai.evaluations --source local
```

```bash
go test ./internal/...   # unit tests
```

### Live integration tests

These talk to a real Foundry project, so they are excluded from the default
build by the `live` tag and additionally gated on an environment variable:

```bash
export AZURE_AI_EVAL_E2E_LIVE=1
export FOUNDRY_PROJECT_ENDPOINT=https://<account>.services.ai.azure.com/api/projects/<project>
export AZURE_AI_EVAL_MODEL=gpt-4.1-nano       # optional judge model
export AZURE_AI_EVAL_AGENT=<agent-name>       # optional, enables the run phase

go test -tags live ./internal/cmd/ ./tests/live/ ./tests/cli/
```

`./tests/cli/` drives the built binary and checks command wiring as well as
package behavior.

The hero walkthrough is behind its own tag, because it scaffolds a project
end to end:

```bash
go test -tags hero ./tests/hero/
```

They clean up every resource they create.

### Debug logging

Request tracing is off by default. `--debug`, or `AZD_EXT_DEBUG=true`, writes it
to a dated log file in the temporary directory rather than the terminal, and
prints that path on stderr.

## Telemetry

When installed from the official registry, the extension reports the
`init.completed` usage event once `azd ai eval init` has written a scaffold and
wired the eval service into `azure.yaml`. Its `ext.source` attribute is exactly
one of:

- `traces` when the eval will grade rows read from project telemetry;
- `dataset` when it will grade rows from a declared dataset; or
- `unknown` for a source this extension does not recognize.

The event is reported after the scaffold is committed, not while the prompts
run, because a confirmation can send the reader back through the questions and
only the last pass describes what was written.

Event names, attribute keys, and their finite value sets live in
`internal/telemetry/events.go`. Do not call `ReportUsage` directly from command
code, and never include eval, dataset, evaluator, or agent names, file paths,
project endpoints, model deployments, or anything else a user typed. Failures
and their structured error codes are already reported separately by the
extension SDK; this event records usage only.

## Installation and release prerequisites

Registry installation resolves `azure.ai.evaluations` through its entry in
`cli/azd/extensions/registry.json`. A `microsoft.foundry/extension.yaml`
dependency requires that registry entry to resolve. Local installation uses
`azd x pack`, `azd x publish`, and the local extension source.

Release execution requires a registered Azure DevOps pipeline under
`azure-dev/extensions` with access to the shared release infrastructure;
a YAML file alone does not register a pipeline.
The `ext-azure.ai.evaluations` issue label routes extension issues.
