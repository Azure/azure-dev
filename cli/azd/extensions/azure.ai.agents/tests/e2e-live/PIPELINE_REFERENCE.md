# Azure AI Agents Live E2E Pipeline Reference

This note documents how to manually validate the Tier 2 live E2E pipeline for
the `azure.ai.agents` azd extension from a pull request. It is intended as a
quick reference for contributors and reviewers.

## Pipeline

- Name: `ext-azure-ai-agents-live`
- YAML: `eng/pipelines/ext-azure-ai-agents-live.yml`
- Scope: Tier 2 live golden path against real Azure resources
- Flow: `azd ai agent init` -> `azd provision` -> `azd deploy` ->
  `azd ai agent invoke` -> `azd down`
- Deploy modes: `code`, `container`, or `both`
- Default service connection: `azure-sdk-tests`
- Default location: `eastus2`

## Related PRs

- Initial live pipeline and Go PTY E2E driver:
  https://github.com/Azure/azure-dev/pull/8758
- 1ES pipeline artifact fix and post-creation validation fixes:
  https://github.com/Azure/azure-dev/pull/8998

PR #8758 added the live Tier 2 pipeline and the Go pseudo-terminal driver that
drives the interactive `azd ai agent init` wizard. PR #8998 fixed the 1ES
artifact publishing shape after the pipeline was created in Azure DevOps and
validated the pipeline end to end from a PR branch.

## Triggering From A PR

The pipeline is intentionally not part of automatic PR validation because it
uses live Azure resources. To run it from a PR, add this comment to the PR:

```text
/azp run ext-azure-ai-agents-live
```

The Azure Pipelines bot should queue a run for the PR branch. In the run summary,
confirm the `self` repository points to the PR branch or PR merge ref you intend
to validate.

## Scheduled Run

The pipeline also runs weekly on `main`:

```yaml
schedules:
  - cron: "0 7 * * 1"
    displayName: Weekly live golden-path E2E
    branches:
      include:
        - main
    always: true
```

This means it runs every Monday at 07:00 UTC even when there are no new changes.

## Success Criteria

For the default `both` mode, the live test should show both deploy modes passing:

```text
--- PASS: TestTier2Live
    --- PASS: TestTier2Live/code
    --- PASS: TestTier2Live/container
PASS
```

The test validates that the deployed agent can answer a simple invocation:

```text
2 + 2 = 4
```

The test and the pipeline cleanup step both run `azd down --force --purge` to
avoid leaking resources. Resource groups are also tagged with `DeleteAfter` as a
fallback for EngSys cleanup.

## Notes For Reviewers

- Yellow warnings from auto-injected Secure Supply Chain Analysis may appear for
  existing repository-wide package feed or Dockerfile findings. These warnings
  are separate from the live E2E test result.
- If the PR comment does not queue a run, check Azure Pipelines PR comment
  trigger permissions and pipeline registration.
- If the run queues but uses the wrong source branch, check the ADO run summary
  before interpreting the test result.
- If the run reaches `TestTier2Live`, failures should usually be investigated by
  phase: `init`, `provision`, `deploy`, `invoke`, or `down`.
