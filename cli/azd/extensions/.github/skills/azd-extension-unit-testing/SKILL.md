---
name: azd-extension-unit-testing
description: "Write, update, and review feature-aligned Go unit tests for azure.ai.agents, azure.ai.connections, azure.ai.inspector, azure.ai.projects, azure.ai.routines, azure.ai.skills, and azure.ai.toolboxes. Use for unit test cases, regression tests, acceptance-criteria mapping, mock assertions, and unit coverage gaps. Establish expected behavior from requirements, not the current implementation alone. Not for CLI subprocess tests, recorded HTTP playback, live integration, E2E, CI changes, or skill migration."
---

# AZD Extension Unit Testing

## Purpose and boundaries

Detect feature regressions through focused, deterministic unit tests.
Higher coverage is not a substitute for meaningful assertions.

Work only on the requested feature and extension. Read applicable `AGENTS.md`
and existing tests first. Reuse the module's framework, mocks, and helpers.
Do not add dependencies, change CI, migrate skills, or modify production behavior
unless separately requested. Report defects exposed by a tests-only task.

## 1. Define the expected behavior

- Identify the user goal and its requirement source: acceptance criteria, test
  plan, issue/specification, documented contract, or existing supported behavior.
- Read the implementation to locate the behavior and test seams, not to derive
  the expected answer from whatever the implementation currently does.
- State the relevant Given / When / Then: preconditions, action, result, and any
  forbidden side effects. Keep this brief; no separate plan file is required.
- If requirements are clear, proceed. If a material expectation is missing or
  conflicts with current behavior, ask for clarification instead of inventing it.
- If the user asks only for a test plan or review, do not implement tests.

Choose cases by impact and the change: success, relevant invalid inputs, boundary
conditions, error propagation, and state transitions. Do not generate a full
parameter matrix or add tests solely to increase coverage.

## 2. Keep the test at unit scope

- Call the relevant production logic in-process. Mock or fake external boundaries
  such as Azure clients, credentials, the clock, or process execution as needed.
- Do not mock the function under test or assert only a value configured on a mock.
- Do not contact Azure, use developer credentials, create resources, replay HTTP
  recordings, or launch azd/extension binaries.
- Use synthetic inputs and existing in-memory fixtures. Where file behavior
  requires real files, use isolated temporary directories and describe that
  boundary; do not claim this proves the installed CLI works.
- If the required claim needs a real process or service, report that it belongs
  to another test level rather than silently expanding this task.

## 3. Assert the feature contract

- Use Arrange / Act / Assert and behavior-based test names. Use Go table-driven
  cases when they improve clarity, not to hide unrelated behaviors in one test.
- Derive expected values independently. Do not use the same production logic
  for both the actual result and the expected result.
- Assert relevant values, error codes/content, state, and side effects.
  "No error," "no panic," or nonempty output alone rarely proves a feature.
- Verify meaningful interactions, such as no mutation request after validation
  fails. Avoid private call-order assertions unless ordering is the contract.
- Check structured output semantically. Require exact text only when exact
  wording is a supported contract.
- When applicable, cover explicit flag conflicts, defaults, tenant selection,
  redaction, cancellation/timeout decisions, and local state cleanup. Do not
  treat these as mandatory cases for unrelated features.
- Use realistic response shapes and make unexpected mock calls fail.

**Example contract:** Given conflicting inputs to `azd ai agent invoke`, input
validation returns the defined error and does not call the service client.
Exercise the in-process validation/action; do not deploy an agent for this test.
Confirm the actual command contract before choosing the conflicting inputs.

For AI-authored tests, check whether a plausible wrong result would still pass.
A constant result, ignored input, swallowed error, or skipped side effect should
be rejected when it violates the selected scenario.

## 4. Keep tests independent and maintainable

- Do not share mutable user configuration or depend on test order.
- Use the repository's temporary-directory and cleanup helpers. Restore modified
  process state; do not combine global environment mutation with parallel tests.
- Avoid arbitrary sleeps and unbounded retries. Use fake time or controlled
  state transitions where timing is part of the behavior.
- Keep secrets and customer data out of fixtures and failure output.
- Do not weaken assertions, change expected results, or add skips merely to
  match a product defect. Harmless internal refactors should not break tests.

## 5. Verify the tests and report evidence

- Run the smallest relevant tests using the repository's configured runner.
  Record the exact command and outcome.
- For a bug regression, verify failure on the unfixed behavior and success after
  the fix when safely feasible. Do not disturb unrelated work to obtain this
  evidence; report when the failing baseline was not verified.
- A compile error, missing fixture/tool, or environment failure is not evidence
  that an assertion detects the intended defect.
- Check module boundaries, build tags, and platform constraints. A local pass
  does not prove CI selects the test; nested Go modules may need separate runs.
- If measuring coverage, state the tested module, revision, platform, and
  exclusions. Use it to identify relevant gaps, not to claim functional
  completeness or average unrelated package percentages.

Summarize: requirement/scenario tested, assertions added, commands run and
results, skipped or unverified cases, and whether existing CI executes the tests.
Do not claim owner approval, CI execution, or complete coverage without evidence.

## References

- [Microsoft unit testing best practices](https://learn.microsoft.com/en-us/dotnet/core/testing/unit-testing-best-practices)
- [Google unit testing: maintainable behavior-focused tests](https://abseil.io/resources/swe-book/html/ch12.html)
- [Google code review: useful tests and assertions](https://google.github.io/eng-practices/review/reviewer/looking-for.html#tests)
