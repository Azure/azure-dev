---
name: azd-extension-spec-authoring
description: >-
  Draft, update, and statically review user-facing feature and experience specs
  for azd extensions. USE FOR: CLI or YAML contract changes, acceptance
  criteria, and checking readiness for parallel product and test development.
  DO NOT USE FOR: implementation, executable test generation, live runs, CI
  design, or architecture decisions alone.
license: MIT
metadata:
  version: "1.0"
---

# azd Extension Spec Authoring

Read the applicable repository guidance, feature brief, and any existing spec.
Derive expected behavior from the agreed user contract, not the implementation.

## Minimum content, not a fixed outline

- **Goal:** Say what the user needs to accomplish and why the change matters.
- **User-facing behavior:** Describe what the user does and what the feature
  promises. Include public commands, flags, or YAML inputs when they matter.
- **Acceptance:** Say how to prove each material promise through a public
  interface. A short change can combine the behavior and its acceptance example.

Keep the existing outline or use these three labels for a new spec. Add non-goals,
compatibility, error cases, permissions, cleanup, or open decisions only when
they change what someone must build or verify. Omit empty sections.

## Write only the scenarios needed for handoff

Start with the main workflow. Add a separate case only if it tests a distinct
promise or meaningful risk. Write the starting state, concrete user action or
steps, and observable result. Use Given/When/Then when helpful, not as required
syntax. For example:

> **Given** a Responses agent is selected, **when** the user runs
> `azd ai agent invoke "message" --no-wait` without `--long-running`,
> **then** the CLI fails and explains that `--no-wait` requires `--long-running`.

Use the smallest test boundary that proves the behavior. Reserve live E2E for
promises that require a real service interaction. State any required environment
capability, not the fixture implementation or CI setup. Keep P0/P1 owner-defined.

Before handoff, check that a developer can implement the public behavior without
guessing and that a test author who has not seen the implementation PR can
identify the inputs, steps, and assertions that would reject incorrect behavior.
Ask the owner about unresolved behavior instead of inventing it; report missing
fixtures separately. Hand off a spec link and scenario anchor; add stable IDs
only when tracking benefits from them, and preserve existing IDs. Link any
tracking issue rather than copying the steps.

Keep spec work at the documentation level. For review-only requests, report gaps
without editing. Do not generate test code, run live operations, or change CI.
