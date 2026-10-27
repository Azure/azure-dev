---
name: extension-user-contract-review
description: "**WORKFLOW SKILL** - Inspect every first-party azd extension PR diff for user contract changes before general code review. USE FOR: review extension PR, check extension contract changes, request experiences spec, review extension flags or schemas, audit contract review coverage. DO NOT USE FOR: core-only PRs, implementing extensions, running tests, or protobuf generation outside PR review."
license: MIT
metadata:
  version: "1.1"
---

# Extension user contract review

**REVIEW SKILL** - Invoked by the PR reviewer; use read-only repository and GitHub tools.

Inspect every changed file under `cli/azd/extensions/**` before general review lenses, including fork PRs.
This is a mandatory review pass, not an optional lens selected after spotting a contract change.

## Procedure

Read and apply [the review procedure](references/review.md) in full. Use the complete diff and review history to:

1. Account for every changed extension file in an internal coverage ledger.
2. Identify observable contract changes, including intentional, correctly implemented additions.
3. Match existing threads and approved experiences spec links per finding.
4. Return one diff-line inline finding per distinct new contract change, preserving `side` and `line`:
   `RIGHT` uses new-file coordinates; deletion-only `LEFT` uses old-file coordinates.
5. Audit coverage and actual outgoing comments after triage; return `complete`, `blocked`, or `not-applicable`
   with file, new-comment, and existing-thread counts.

The host posts comments and exposes the completion status. Do not execute PR code or mutate GitHub.
Do not treat a "refactor" label, tests, documentation, or a spec link in another thread as an exemption.
Preserve findings through ordinary noise filters and comment limits.

## Error handling

If evidence, a required skill, an anchor, or output capacity is missing, report `blocked` and the specific limitation.
Never silently skip the pass, fabricate a missing spec, or claim a clean review.

## Examples

A tested new flag still needs a spec thread. An unchanged finding already reported needs no duplicate.
A genuine internal refactor produces no contract comment after inspection.
