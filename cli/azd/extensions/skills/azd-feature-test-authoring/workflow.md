# AZD Feature Test Authoring

## Goal and scope

Turn feature requirements into tests that detect incorrect user-visible behavior.
Passing tests and higher coverage are not substitutes for meeting the feature contract.

Apply to the seven extensions named in the description and their shared workflows.
Do not silently expand the feature scope, change product behavior, or introduce a
test framework, dependency, or CI architecture.

Read applicable repository and extension instructions first. Reuse existing test
helpers, fixtures, error contracts, runners, and scenario conventions.

## 1. Establish the expected behavior

Before writing assertions, identify:

- The user goal and affected feature.
- The source of expected behavior: supplied test plan, acceptance criteria,
  issue/specification, documented service contract, or existing supported behavior.
- Observable success, relevant failure behavior, and actions that must not happen.
- Scope exclusions, prerequisites, and uncertainties.

Read implementation to understand execution paths and seams, not to manufacture
the expected result. Distinguish required behavior from current behavior.
Do not infer acceptance criteria from an AI-generated implementation alone.

**Use a lightweight, risk-based workflow:**

- Clear, narrow change: reuse its acceptance criteria and proceed with focused tests.
- Multi-component, asynchronous, destructive, or live-service change: outline the
  relevant scenarios and prerequisites before coding.
- Missing or conflicting expectations: ask the user/feature owner to resolve the
  material ambiguity. Label proposals as proposals; do not claim owner approval.
- Supplied, sufficiently clear test plan: use it; do not demand another document
  or an unnecessary approval round.

Keep the plan in the response unless a file is requested or the repository
explicitly requires one. For each selected scenario, record:

| Scenario | Requirement source | Given / When / Then | Level and dependencies | Test / execution path |
|---|---|---|---|---|
| Stable ID and user behavior | Link or specific criterion | Preconditions, action, observable outcome | Unit, CLI/integration, or E2E; fake/playback/live | Existing or proposed test and runner |

Use concrete examples, not "verify it works." A small change may need only one row.
The map must show what is covered, deferred, not applicable, or still uncertain.

## 2. Select scenarios and test levels

Prioritize by user impact, integration risk, previous defects, and the change.
Consider success, failure, boundaries, and state transitions where relevant;
do not mechanically generate every combination.

Choose the narrowest level that can detect the intended failure:

| Level | Appropriate use | What it does not establish |
|---|---|---|
| Unit | Input rules, parsing, transformations, decisions, errors, state transitions | Real process wiring, permissions, or service compatibility |
| CLI / integration | Command discovery, argument binding, host-extension interaction, output, generated files | Live compatibility when external responses are faked or replayed |
| Live integration / E2E | Actual service interaction or a representative user workflow spanning real dependencies | Exhaustive command, parameter, protocol, or sample coverage |

CLI is an entry point, live describes dependencies, and PR/weekly describe timing.
Do not classify a test by its filename, framework, or schedule alone.
Calling Cobra in-process does not prove that the built extension launches.

An E2E scenario must name the user goal and final result, not just list commands.
A command is not high priority simply because it is untested or easy to append.
Do not require every extension to have an independent CLI suite or live E2E.

For each scenario, check whether a sample is needed. A project, resource
definition, test file, local server, or existing deployment may be sufficient.
Verify protocol, deployment mode, and service prerequisites before reusing a sample.

## 3. Write behavior-based tests

### Assertions

- Express the contract independently of the implementation. Use explicit expected
  values or independently derived invariants, not the same production function
  to compute both expected and actual output.
- Exercise the production behavior, not only fixture setup or a mocked return value.
- Assert meaningful outputs, errors, persisted state, or observable side effects.
  Exit code 0, nonempty output, or "no panic" alone is insufficient for a workflow.
- On validation failure, check the relevant error and absence of forbidden
  mutations or service requests when that is part of the contract.
- Check interactions when meaningful: for example, no create call after invalid
  input. Do not couple tests to harmless private call order or incidental helpers.
- Assert stable error codes, essential message content, JSON fields/types, and
  generated configuration semantics. Require exact text only when text is the contract.
- Do not weaken assertions, change fixtures, update snapshots, or add skips merely
  to accommodate a defect. If the task is tests-only, report the product defect
  instead of silently changing production code.

### Relevant AZD contracts

Select these based on the feature, not as a mandatory checklist for every test:

- Explicit arguments/flags take precedence; explicitly set flags that cannot
  take effect must be rejected rather than silently ignored.
- `--no-prompt` must complete or fail clearly without unexpected interaction.
- JSON output must follow the actual command contract; status text must not
  corrupt it. Use full, verified English commands with required inputs.
- Errors must not become success-shaped results; injected output writers and
  existing error-handling conventions must be respected.
- Guest/multi-tenant credentials use the user access tenant when applicable.
- Destructive operations must match the service contract, handle permitted empty
  responses, and clean relevant local IDs/state as well as remote resources.
- Asynchronous operations require explicit pending, terminal, timeout, or cancel
  expectations as applicable. A cancellation request being accepted is not proof
  that the operation has reached a cancelled terminal state.
- Never disclose credentials or user data in outputs, fixtures, recordings,
  assertions, or artifacts. Use synthetic data and non-disclosure assertions
  where credential-bearing values can reach output.

### Fixtures and doubles

- Mock external boundaries needed for the scenario, not the behavior under test.
- Use realistic request/response shapes from a known contract. Fail unexpected
  requests rather than supplying a generic success response.
- Avoid shared developer configuration, mutable global resources, fixed ports,
  test-order dependencies, and wall-clock assumptions.
- Use temporary directories, isolated config, bounded waits, fake clocks, or
  controlled state transitions as supported by existing helpers.
- For playback, record provenance and intended contract. Manually triggered,
  reviewed recordings are not proof that the live service is currently compatible.
  Do not blindly re-record a failing test to turn it green.

## 4. Validate that the tests can detect the failure

For a bug regression, run the new test against the unfixed behavior when feasible,
then verify it passes after the fix. For new behavior, test before implementation
where practical; otherwise identify the specific incorrect behavior the assertion
would reject.

An expected assertion failure is useful evidence. Compilation errors, missing
fixtures, unavailable tools, network failures, or authentication failures do not
prove that a regression test detects the intended defect.

Do not modify a shared working tree merely to demonstrate a failing baseline.
Use a safe isolated comparison if needed. If red/green evidence is unavailable,
say so; do not claim it was verified. Mutation testing is optional, not a required
new tool or a substitute for valid requirements.

For AI-authored tests, explicitly check:

- Did the expected behavior come from a requirement rather than a guessed rule?
- Could the test pass if the target behavior returned a constant or skipped its work?
- Does the test distinguish success from a plausible wrong output or state?
- Would a harmless internal refactor break it?

## 5. Verify execution and CI wiring

- Run the smallest relevant configured tests. Report the exact command and result.
  Follow repository build prerequisites; do not install new tooling unnecessarily.
- Inspect the module boundary, build tags, live gates, platform constraints, test
  selectors, and prerequisite binaries. A new test file is not proof CI runs it.
- Nested Go modules need separate execution when the parent pattern excludes them.
- Identify whether the change stays inside the extension folder or requires
  external functional tests, shared pipeline changes, installation, or authorization.
  Flag external changes before making them unless already authorized.
- Prefer existing suite entry points when they genuinely fit. Do not force
  unrelated workflows into Agent E2E or rename tests solely to bypass a selector.
- Treat required, selected scenarios with missing prerequisites as blocked/failed
  according to the suite contract. Report optional or platform skips separately;
  skipped and not-applicable are not passed.

**Live execution is opt-in.** Do not create resources, authenticate, invoke
billable services, record live traffic, or run cloud-backed scenarios without
explicit authorization for that execution and environment. Preparing a live test
does not authorize running it. Register cleanup before mutations, isolate names
and IDs, bound timeouts, and preserve cleanup failures and first-attempt failures.

Use coverage to find unexecuted relevant paths, not to rank features or inflate
numbers. State the revision, selected modules, platform, tags, exclusions, and
measurement scope. Do not average package percentages to obtain a module total.
Do not claim higher coverage means stronger assertions or complete feature coverage.

## Completion summary

Return briefly:

1. Requirement/scenario to test mapping and why this level was chosen.
2. Tests run and outcomes; identify blocked, skipped, deferred, or unverified cases.
3. Regression evidence, where available; explain material limitations.
4. CI execution path and any required changes outside the extension folder.

Do not claim full feature coverage, owner approval, CI execution, or live success
without evidence.

## Practice basis

- [Microsoft testing strategy](https://learn.microsoft.com/en-us/azure/well-architected/design-guides/testing):
  risk-based scope, critical workflows, and explicit test plans.
- [Cucumber BDD](https://cucumber.io/docs/bdd/):
  shared examples and agreed behavior before automation; no Cucumber dependency required.
- [Google code review: Tests](https://google.github.io/eng-practices/review/reviewer/looking-for.html#tests):
  appropriate tests with changes, useful assertions, and detection of broken behavior.
- [Google unit testing](https://abseil.io/resources/swe-book/html/ch12.html):
  fast feedback and maintainable behavior-focused tests.
- [Microsoft unit testing best practices](https://learn.microsoft.com/en-us/dotnet/core/testing/unit-testing-best-practices):
  isolation, repeatability, and the limits of coverage.
