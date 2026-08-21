# Known gaps

Open work on `azd ai eval`, written down so it is decided rather than
rediscovered. Each entry says what is wrong, why it was not fixed where it was
found, and what closing it involves.

## 1. The editing path should not go through typed structs

**The one that matters.** Everything in section 2 is a symptom of this.

`generate`, `init` and the catalog commands read `evals/azure.eval.yaml` into
Go structs and write it back. Two consequences:

- **The file is rewritten, not edited.** Every comment is deleted and the
  indentation is normalised. An author who annotates their configuration loses
  those notes the first time they run `generate`.
- **`$ref` has to be modelled shape by shape.** azd core resolves the directive
  on *any* object at any depth, but a typed round-trip only survives it where a
  `Ref` field exists. It is currently on `DatasetDecl`, `EvaluatorDecl` and
  `Eval`, and each addition needed a specification change. Anywhere it is
  missing, the file deploys and the editing commands refuse it.

azd core already ships the mechanism: `foundry.YAMLDocument` in
`cli/azd/pkg/foundry/includes_edit.go` is comment-preserving and `$ref`-aware
(`EntryRef`, `EditRefFile`, `SetServiceField`), and `cli/azd/pkg/yamlnode`
provides `Find` / `Set` / `Append` over a node tree. `YAMLDocument` has no
callers today; its own doc comment says it was written for the composition
command write path.

Moving the editing commands onto it would preserve the author's file, make the
directive work wherever core supports it, and let the three `Ref` fields and the
`splicedEvaluators` / `nestSplicedRubrics` machinery be deleted. `YAMLDocument`
is oriented around service entries in `azure.yaml`, so this needs either a
generalisation there or direct use of `yamlnode`.

## 2. Gaps that item 1 would close

### 2a. `$ref` on nested shapes deploys but cannot be edited

`$ref` under an eval's `source:`, under its `target:`, or on an item of its
`evaluators:` list resolves and deploys correctly, then fails the editing read
with `unknown key "$ref"`.

Not fixed in place because modelling it means adding a fourth, fifth and sixth
`Ref` field, which `internal/project/config_keys_test.go` deliberately fails:
those tests pin each shape to the specification, and a new key there is a
promise the spec does not make. It also needs validation changes, since a
reference carrying only a directive currently fails `evaluator entry is missing
'evaluator'`.

Two ways to close it without item 1: extend the spec to allow the include on
those shapes, or refuse it on the resolving path so both reads fail together.
The second would forbid something item 1 intends to support, so it is not
recommended.

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

### 3a. `init` can append a duplicate eval behind a `$ref`

`init` checks only the unresolved configuration, so a pure-`$ref` eval decodes
with an empty name and `HasEval` misses it. `init --name nightly` then appends a
second entry, reports success, and the next resolving read fails naming a
duplicate the author cannot see.

The catalog commands already refuse this for datasets and evaluators via
`checkCatalogEntryIsEditable`; `init` has no equivalent and `catalogEntryShapeOf`
has no `eval` branch. Related: `init --force` calls `RemoveEval`, which on an
overlay-name include deletes the directive and orphans the referenced file
without saying so.

### 3b. A bare rubric's own `name` becomes the catalog name

A `$ref` pointed straight at a rubric file splices that file's `name` into the
entry, so the rubric names the evaluator. Intended for the documented layout,
surprising if the rubric was written for something else.

### 3c. `errors.As` and `sort.Strings` predate the modernization guidance

`cli/azd/AGENTS.md` asks for `errors.AsType[T]` and the `slices` equivalents.
Neither is rewritten by `go fix -diff`, which is what
`lint-ext-azure-ai-evaluations.yml` actually gates on, so this is convention
rather than a build failure. Pre-existing across this extension and
`cli/azd/pkg` (40 and 5 occurrences). Worth one sweep, not a change per PR.

### 3d. The schema cannot express every version conflict

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
