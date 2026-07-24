# Live Pipeline Comment Trigger Test

This temporary note exists to make this test PR match the live pipeline path
filters. After PR #9039 is merged, use this PR to verify that the live pipeline
can be queued from a GitHub PR comment.

Full pipeline name:

```text
/azp run azure-dev - live - ext - azure.ai.agents
```

Short name to try if needed:

```text
/azp run ext-azure-ai-agents-live
```

Expected result: Azure Pipelines queues definition 8268 and does not reply with
either of these errors:

```text
No pipelines are associated with this pull request.
Azure Pipelines could not run because the pipeline triggers exclude this branch/path.
```

If allowed to run to completion, the live test should end with:

```text
--- PASS: TestTier2Live
    --- PASS: TestTier2Live/code
    --- PASS: TestTier2Live/container
PASS
```

ADO pipeline summary:
https://dev.azure.com/azure-sdk/internal/_build?definitionId=8268&_a=summary
