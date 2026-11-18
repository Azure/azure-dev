---
name: azd-extension-e2e-testing
description: >-
  Write, update, and statically review executable, feature-level E2E tests for
  Foundry extensions in Azure/azure-dev. Translate test plans and acceptance
  criteria into real CLI tests with meaningful assertions and isolated fixtures.
  USE FOR: E2E scenario confirmation, test-plan-to-code implementation, live
  CLI regression tests, feature coverage gaps, and E2E assertion review.
  DO NOT USE FOR: product spec authoring, unit-only tests, automatic live
  execution, interactive-scenario YAML authoring, CI redesign, or skill migration.
license: MIT
metadata:
  version: "1.2"
---

# AZD Extension E2E Testing

## Purpose and boundaries

Turn an agreed feature contract into executable E2E test code. Confirm the
scenario without creating a second independently maintained test plan. Keep
feature assertions separate from environment provisioning and CI orchestration.
Use the repository's existing language, framework, and helpers; the current
agents live suite uses Go.

Read applicable `AGENTS.md` files, the source spec or plan, and neighboring tests.
Change only the requested tests and necessary test fixtures. Do not modify
product behavior, CI, credentials, labels, or execution policy unless separately
requested. Report product defects exposed by a tests-only task.

Writing tests does not authorize live execution, Azure authentication, deployment,
or deletion. Run live tests only when explicitly requested and the applicable
execution workflow permits it, with the target, ownership, and cost understood.
Never bypass live opt-in gates or automatically run interactive scenarios.

**Review-only tasks are static-only:** do not edit files, build, run tests,
install tools, or execute PR code. Return findings in the caller's review format.
The authoring workflow below does not override these restrictions.

## 1. Map the plan to observable behavior

- Identify the requirement source, feature, scenario, owner-provided priority,
  and existing coverage. Link the source in the test or its existing tracking
  location; do not create a second independently maintained plan.
- Derive expected behavior from requirements or supported public contracts,
  not solely from the current implementation. Ask about material ambiguity
  instead of inventing flags, response fields, APIs, timing guarantees, or
  snapshot behavior.
- Describe the initial state, action, observable result, and forbidden side
  effects briefly. Map each material expectation to an assertion. Flag steps
  that cannot yet be observed through supported interfaces.
- Preserve owner priorities. Do not infer P0 from deployment/invocation or P1
  from an optional flag, special configuration, cost, or implementation effort.
  Do not equate P0/P1 with execution frequency or release gates.
- Reuse existing basic hosted code/container coverage. Add a distinct feature
  assertion or missing variant rather than another identical golden path.
- Keep each test focused on one coherent behavior. Share setup through fixtures;
  do not force every feature through init/provision/deploy/invoke when its
  contract concerns a different operation.

### Confirm scenarios before writing code

- Read the referenced spec, existing plan or task, and relevant clarifications.
  If a material expectation is unconfirmed or conflicts across sources, ask the
  requester for the source or the decision. Wait before writing affected tests.
- For a new or materially changed scenario, show a short plan in the
  conversation by default: the source and scenario, setup and inputs, user
  steps, observable pass criteria, and any fixture or cleanup dependencies.
  If the user has not confirmed that plan, end the turn with the preview and
  ask for confirmation. Do not edit test files in that turn, even if the
  request says to implement the test or the product spec is already agreed.
  An already confirmed plan covering the same steps and outcomes does not
  need another approval.
- Do not require a GitHub issue or routinely ask whether to create one.
  Suggest separate tracking only when it helps with handoff, deferred work,
  or a fixture or CI blocker. Prefer an existing task. If a new draft is
  useful, name it `[Feature] <scenario>`. Link the spec and scenario instead of
  copying the steps. Do not create or edit an issue without explicit permission;
  confirming a plan does not authorize publication or live execution.
- Keep plan-only requests at the plan stage. Link generated tests to the agreed
  requirement source even when there is no issue. Distinguish implementation,
  offline validation, live execution, and CI evidence in any status update.

## 2. Establish the real E2E boundary

- Exercise the actual CLI/extension binary and the real service boundary needed
  to prove the selected contract. State the tested binary revision and target.
- Do not mock the feature under test, replace a required live interaction with
  recorded playback, or report unit/integration coverage as full E2E coverage.
  Isolate unrelated dependencies only when the reduced boundary is explicit.
- Declare only the necessary capabilities: platform, enabled product features,
  supported hosting/protocol modes, permissions, model availability, network
  reachability, and resource lifecycle. Use existing fixture/configuration
  mechanisms; do not introduce a new metadata framework for a single case.
- Keep actual subscriptions, tenants, regions, model deployments, service
  connections, and secrets in injected environment profiles. A region or model
  constraint that is itself the feature contract remains a test requirement.
- Distinguish prerequisites from the behavior under test. Missing access must
  not be interpreted as the expected product error in a policy-negative test.

## 3. Reuse fixtures without coupling to CI

- Inspect the current harness before using it. Reuse compatible subprocess,
  authentication, temporary-directory, readiness, diagnostic, and cleanup
  helpers. Do not invent helper APIs or copy an entire runner into a new test.
- Use a private working directory and configuration/session state, unique
  run-scoped resource names, and allocated ports. Never modify a developer's
  original authenticated configuration or unrelated persisted state.
- Keep tests independent of execution order. Shared expensive fixtures are
  acceptable only with explicit ownership, isolated mutable state, and a
  lifecycle that prevents cascading failures.
- Use existing opt-in and configuration gates. Ordinary test discovery must
  not create cloud resources. Once a required live suite is explicitly enabled,
  missing prerequisites must be reported as failures or blocked execution, not
  silently counted as successful coverage.
- If shared infrastructure lacks a necessary capability, identify the exact
  gap and smallest reusable extension. Keep CI/runner redesign separate unless
  requested; do not reduce assertions or add success-shaped fallbacks.

## 4. Implement meaningful assertions

- Use behavior-oriented Go test/subtest names and Arrange / Act / Assert.
  Check command exit status and the semantic result, relevant persisted state,
  and required or forbidden side effects. "No error" or nonempty output alone
  usually does not establish feature correctness.
- Parse structured output when available. Require exact text only for a defined
  textual contract or a deliberately deterministic test fixture.
- Give every invoke explicit input. Define input-file contents when testing file
  input; do not rely on an interactive message prompt that does not exist.
- For model-backed responses, prefer supported protocol fields, controlled
  inputs, deterministic test-agent behavior, and observable state changes over
  verbatim generated prose. Do not use an uncalibrated model judge as a default
  pass condition.
- Verify that plausible wrong behavior would fail: ignored configuration,
  wrong resource/session, local fallback, a stale result, swallowed errors, or
  incomplete state transitions. Do not derive expected results using the same
  production transformation under test.
- For expected failures, assert the specific failure and absence of forbidden
  mutations. An authentication, quota, network, or fixture failure is not proof
  of the intended validation error.

Examples of assertion scope:

| Scenario | What the test must actually demonstrate |
| --- | --- |
| Snapshot-enabled deploy/invoke | Configuration compatibility and successful invocation; do not claim snapshot creation/restoration without observing it. |
| Snapshot restoration | A supported signal that restoration occurred and the required restored behavior; ask if that contract is not defined. |
| Native A2A invocation | The actual A2A path and expected response from this run's target; stop a local server when proving the remote path. |
| Long-running invocation | Response identity, state, and completion/follow behavior; a quick request alone does not establish long-duration resilience. |

## 5. Bound asynchronous behavior and failures

- Poll observable readiness/state with context deadlines, bounded intervals,
  and terminal-error detection. Do not introduce arbitrary fixed sleeps.
- Retry only documented transient conditions within a budget. Keep original
  failures and attempts visible. Do not blindly retry mutations that may have
  succeeded, or retry assertions until an unrelated answer happens to match.
- Bound each subprocess and cancel its process tree through existing helpers.
  Preserve timeout, cancellation, and nonzero-exit evidence.
- Capture relevant command output, resource identity, and service diagnostics
  before teardown. Redact credentials, credential-bearing URLs, and sensitive
  data before logging or publishing artifacts.

## 6. Make ownership and cleanup explicit

- Track what the test created, borrowed, or adopted. Register cleanup immediately
  after each successful allocation, including failure paths.
- Delete only resources owned by this test, using persisted identities rather
  than reconstructed default names. Validate the resource-group boundary before
  group-level teardown; never delete a shared project or borrowed infrastructure.
- Use a separate bounded cleanup context: `t.Context()` is canceled before
  cleanup runs. Clean up applicable local conversation/session state and
  subprocesses. Preserve original test failures while surfacing cleanup failures
  and residual resource identities; never silently ignore resource leaks.
- Retain live resources only when explicitly requested by the caller or test
  contract. Report retained resources, continuing costs, ownership, and the
  cleanup procedure. Do not copy a retained/manual fixture into ordinary CI.

## 7. Validate code and report execution separately

- For authoring, use the smallest applicable offline compile/lint/helper checks
  supported by the repository. Check nested Go modules, platform build tags,
  fixtures, and binary selection. Do not install tools without a missing-tool
  failure or an explicit dependency change.
- Where feasible, exercise new assertion helpers with deterministic valid and
  invalid inputs. A plausible wrong result must fail for the intended reason;
  compilation alone does not establish assertion quality.
- Execute the selected live case only with the separate authorization described
  above. Report the exact command, revision, environment class, and outcome;
  distinguish product, prerequisite, infrastructure, and cleanup failures.
- Inspect CI discovery statically: package path, test selector, build tags,
  live gates, required capabilities, and path filters. A locally runnable test
  is not evidence that CI selects or executes it. Inspect the current runner
  and workflow rather than assuming every test in a directory is selected.
- If CI does not select the test, report `integration required` and the specific
  reason. Do not alter workflow YAML, broaden live execution, skip assertions,
  or claim automated CI coverage to make the task appear complete.

For authoring, finish with:

- Requirement and test IDs, code/fixtures changed, and assertions covered.
- Separate evidence for code generated, compile/helper validation, live execution,
  CI selection, and observed CI result. Mark each unverified item explicitly.
- Remaining contract/fixture gaps, required integration, and retained resources.

Do not claim owner approval, CI execution, or complete feature coverage without
evidence. Review-only tasks return the caller's findings format instead.

## References

- [Google: larger tests and realistic system boundaries](https://abseil.io/resources/swe-book/html/ch14.html)
- [Playwright: behavior-focused, isolated tests and retrying assertions](https://playwright.dev/docs/best-practices)
- [Cucumber: preconditions, actions, and observable outcomes](https://cucumber.io/docs/gherkin/reference/)
