# Known gaps

Open work on `azd ai eval`, written down so it is decided rather than
rediscovered. Each entry says what is wrong, why it was not fixed where it was
found, and what closing it involves.

## 1. The editing path no longer goes through typed structs

**Done.** Kept here because the reasoning explains the shape of the code.

`generate`, `init` and the catalog commands used to read `evals/azure.eval.yaml`
into Go structs and marshal the whole thing back. That had two consequences:

- **The file was rewritten, not edited.** Every comment was deleted and the
  indentation normalized, so an author who annotated their configuration lost
  those notes the first time they ran `generate`.
- **`$ref` had to be modelled shape by shape.** azd core resolves the directive
  on *any* object at any depth, but a typed round-trip only survives it where a
  `Ref` field exists.

`internal/project/eval_config_edit.go` now edits the node tree instead, using
`github.com/braydonk/yaml` — the same library azd core uses for `azure.yaml`.
`UpsertCatalogEntry` records one field on a named catalog entry;
`ApplyScaffold` writes the entries `init` decided to add and drops the one
`--force` replaces. Anything neither touches is written back exactly as found,
comments included, whether or not this package models it.

Decisions are still made from the decoded configuration, because that is what
the guards understand; only the write moved. That split is what kept the change
small.

`SaveEvalConfig` now has no callers outside tests and the legacy-path helpers.

Core also ships `foundry.YAMLDocument` (`pkg/foundry/includes_edit.go`), which is
comment-preserving and `$ref`-aware but oriented around service entries in
`azure.yaml`. It has no callers. Worth revisiting if this grows beyond targeted
edits.

## 2. What that closed, and what it did not

### 2a. `$ref` on nested shapes — closed

`$ref` under an eval's `source:`, under its `target:`, or on an item of its
`evaluators:` list resolved and deployed correctly, then failed the editing read
with `unknown key "$ref"`.

Now modelled on `SourceDecl`, `Target` and `evalcore.EvaluatorRef`, allowed by
the schema through the same `FileRef` branch the catalog entries use, and
recorded in `config_keys_test.go`. The decode-time check that an evaluator
reference names an evaluator now allows an entry that is still a directive,
since the file it points at supplies the name — it stays strict on the resolved
reading, where an entry with neither is genuinely malformed.

This changes the shapes the specification describes, so the spec is updated
alongside it rather than the code drifting ahead of it.

### 2b. A rubric rescue is document-wide when the configuration is itself included

`splicedEvaluators` decides per entry whether a spliced rubric should be moved
under `definition:`. When the service entry is itself a `$ref` to an evaluation
configuration, those entries do not exist until after resolution and core does
not report which node came from which include, so the whole evaluator list is
rescued.

The effect is that the same file is stricter opened directly than reached
through the service entry: an entry-level `dimensions:` typo is rejected on one
route and filed as rubric content on the other.

Recovering provenance means either re-implementing resolution — which breaks the
single-resolver invariant that `one_resolver_test.go` enforces, and that
invariant exists because the two read routes disagreeing has caused three
separate bugs — or removing the need for the rescue entirely, which item 1 does.

## 3. Smaller, independent

### 3a. A failed environment read is indistinguishable from an unset key

`evalContext.getEnvValue` returns the empty string both when a key is unset and
when the read failed. It has 17 callers, including the dataset and evaluator
fingerprints, the recorded versions, and the eval IDs.

So a transient azd daemon error reads as "not registered yet". The reconciler
then republishes: a wasted version for an immutable asset, and for a drift check
a recreate nobody asked for. The deploy still reports success.

The fix is to separate state-critical reads, which must surface the error, from
best-effort ones such as the portal link. It is a change of its own because each
caller has to be classified: an eval ID that is genuinely absent on a fresh
clone must keep reading as absent, which is the case the current behavior exists
for, and section 4 below depends on it.

### 3b. `init` can append a duplicate eval behind a `$ref`

`init` checks only the unresolved configuration, so a pure-`$ref` eval decodes
with an empty name and `HasEval` misses it. `init --name nightly` then appends a
second entry, reports success, and the next resolving read fails naming a
duplicate the author cannot see.

The catalog commands already refuse this for datasets and evaluators via
`checkCatalogEntryIsEditable`; `init` has no equivalent and `catalogEntryShapeOf`
has no `eval` branch. Related: `init --force` calls `RemoveEval`, which on an
overlay-name include deletes the directive and orphans the referenced file
without saying so.

### 3c. A bare rubric's own `name` becomes the catalog name

A `$ref` pointed straight at a rubric file splices that file's `name` into the
entry, so the rubric names the evaluator. Intended for the documented layout,
surprising if the rubric was written for something else.

### 3d. `errors.As` and `sort.Strings` predate the modernization guidance

`cli/azd/AGENTS.md` asks for `errors.AsType[T]` and the `slices` equivalents.
Neither is rewritten by `go fix -diff`, which is what
`lint-ext-azure-ai-evaluations.yml` actually gates on, so this is convention
rather than a build failure. Pre-existing across this extension and
`cli/azd/pkg` (40 and 5 occurrences). Worth one sweep, not a change per PR.

### 3e. The schema cannot express every version conflict

`EvaluatorDecl` forbids `version:` alongside `source:` or `definition:`, but an
entry written as a `$ref` matches the `FileRef` branch, whose
`additionalProperties` is `true`, so the conditional never applies.

Left as is deliberately. Whether an overlay `version` conflicts depends on what
the referenced file holds, and JSON Schema cannot see the resolved document.
Forbidding `version` on the evaluator reference shape would reject a legally
pinned evaluator whose referenced file carries only a name. The runtime rejects
the real conflicts with a message naming the entry.

## 4. Known and not a defect

A fresh clone republishes every dataset and evaluator on its first deploy.
Version identity lives in the azd environment, which is not in the repository,
so a new clone cannot know what is already registered.

## 5. Open product question

How much should `init` and `generate` do on the author's behalf, and what should
they leave behind when they fail partway? Six tracked bugs reduce to that one
question and are blocked on it, not on implementation.
