# Voice Agent definition overlay

## Status

- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Feature status:** Experimental
- **Implementation:** Azure/azure-dev#10747

## Goal

Allow advanced Voice Agent GA definition fields that azd does not yet model to
remain declarative and repeatable without expanding the shared typed
`azure.yaml` schema for every low-frequency service field.

Typed `azure.yaml` remains the primary authoring experience. The overlay is an
explicit Voice-only escape hatch and does not replace typed fields, local
validation, editor completion, or existing deploy flows.

## Authoring contract

Inline Voice service:

```text
services:
  voice-agent:
    host: azure.ai.agent
    kind: voice
    name: voice-agent
    modelType: managed
    model:
      id: gpt-realtime
    definitionOverlay:
      $ref: ./voice-advanced.yaml
```

Root Voice definition files may use the YAML alias:

```text
kind: voice
name: voice-agent
modelType: managed
model:
  id: gpt-realtime
definition_overlay:
  $ref: ./voice-advanced.yaml
```

Both aliases are accepted, but they cannot be declared together. Prompt and
Hosted Agents reject either property. A service-level root `$ref` may provide
the final Voice `kind`; resolved validation still rejects overlays when the
referenced definition is not Voice.

## Merge semantics

azd first builds the existing typed GA Voice definition, then applies the
overlay:

- the overlay root must be an object;
- overlay keys use GA wire names;
- an overlay may add a missing key;
- when both sides contain objects, merging recurses;
- an existing scalar or array is a conflict, even when values are equal;
- a new array is accepted as one complete value and is never concatenated;
- `null` is rejected recursively, including inside arrays;
- keys are compared case-insensitively when matching overlay keys against the
  typed definition or an existing typed object, preventing casing variants from
  bypassing conflict detection. Keys inside a wholly new overlay object are
  preserved and receive final validation from the service;
- at the overlay root only, `kind`, `model_type`, `model`, and
  `conversation_engine` are reserved and cannot be added or extended by the
  overlay. The same names may appear inside a newly added nested service object;
- non-finite or integers outside the JSON-safe range are rejected;
- errors identify the conflicting JSON path deterministically.

Create and update/version deployment use the same merged definition.

## Validation boundary

Typed fields keep their existing JSON-schema, Go type, and semantic validation.
The overlay receives only structural and merge-safety validation from azd. The
Foundry Voice Agent service performs final GA contract validation for advanced
fields. Service failures retain normal azd error and request-ID handling.

Overlay `$ref` values reuse the existing local Foundry `$ref` resolver:

- paths resolve relative to the document that contains the `$ref`;
- nested local references and cycle/depth checks are supported;
- remote URL references are rejected;
- local references have the same trusted-input boundary as existing azd
  definition references, including parent and absolute paths;
- inside a referenced overlay, relative string values named `project` are
  rebased from the referenced file to the `azure.yaml` project root. An
  `instructions` value is rebased the same way when it is a single-line `.md`
  or `.txt` path. Inline overlay values are not rebased. These are existing
  shared-resolver semantics, so authors should avoid those key names for
  service fields that are not intended to carry local file paths.

Existing `$ref` sibling semantics apply before the definition overlay merge:
sibling properties shallowly replace the referenced property's complete value.
For example, a sibling `future_config` object replaces the referenced file's
entire `future_config`; it does not recursively merge with it. The recursive
add-only rules above apply only when the resolved overlay is later merged into
the typed Voice definition.

## Compatibility

When the overlay is omitted on any agent, or is a literally empty object on a
Voice Agent:

- azd returns and serializes the existing typed Voice definition directly;
- Managed, BYOM, and Hosted Voice Wrapper behavior is unchanged;
- Prompt and ordinary Hosted Agent behavior is unchanged when the property is
  omitted; either overlay property is rejected when present on those kinds;
- no extra file is read and no merge is performed for an omitted or literally
  empty Voice overlay. An overlay expressed through `$ref` still reads and
  resolves its file even when the resolved object is empty; only the later
  merge is skipped.

Existing six-scenario Voice regression coverage remains the compatibility gate.

## Non-goals

- overriding typed fields;
- array concatenation;
- `null`-based deletion;
- local OpenAPI validation or overlay-specific editor completion;
- changing service API routes, headers, versions, or deployment lifecycle;
- guaranteeing long-term support for every advanced service field.

Frequently used overlay fields can later graduate to first-class typed
authoring based on service/product priority and observed usage.
