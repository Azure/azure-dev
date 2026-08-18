# `azd` Training Extension

An azd extension for Microsoft Foundry training jobs.

## Job inputs and outputs

Local code and input folders use the project's default storage unless a Foundry
storage connection is configured. Set it in the job YAML:

```yaml
storage_connection_name: project-storage
```

or override it for one submission:

```shell
azd ai training job submit --file job.yaml --storage-connection-name project-storage
```

Initial status output, followed by the submitted job as JSON:

```text
Submitting command job: <job-name>

✓ Job '<job-name>' submitted successfully

{
  "id": "<job-resource-id>",
  "name": "<job-name>",
  "properties": {
    "jobType": "Command",
    "status": "<job-status>"
  }
}
```

The command-line option takes precedence over the YAML value. Registered
Foundry dataset IDs using the `azureai://` scheme can be used directly as input
paths and are not uploaded again.

The effective storage connection is included in local-upload deduplication.
Without this distinction, content previously uploaded through default storage
could be reused when a later submission requested a different connection.

To register named output assets, use `asset_name` and optionally
`asset_version`:

```yaml
outputs:
  model:
    type: custom_model
    mode: rw_mount
    asset_name: trained-model
    asset_version: "1"
```

## Design

Internal design notes and execution plans live under [design/](design/):

- [design/design.md](design/design.md) — overall design
- [design/command-job-guide.md](design/command-job-guide.md) — command/job guide
- [design/execution-plan.md](design/execution-plan.md) — execution plan
