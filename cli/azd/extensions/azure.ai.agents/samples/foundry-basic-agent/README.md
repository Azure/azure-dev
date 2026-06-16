# Foundry basic agent — unified `azure.yaml` POC

A single hosted agent on a `host: microsoft.foundry` project, deployable end to end with
`azd up`. This is the shape `azd ai agent init` produces after the unified-`azure.yaml`
rework: **one** `azure.yaml` with the agent declared inline — no `agent.yaml` and no
`agent.manifest.yaml`.

## Layout

```text
foundry-basic-agent/
├── azure.yaml                 # microsoft.foundry service: deployments + one inline hosted agent
└── src/
    └── basic-agent/           # agent source (the agent's `project:`)
        ├── main.py            # code-deploy entry point (startupCommand: python main.py)
        └── requirements.txt
```

## How `azure.yaml` maps to the deploy

- `host: microsoft.foundry` routes the service to the Foundry service target in the
  `azure.ai.agents` extension. A single service represents the whole Foundry project.
- `deployments:` declares the project's model deployments (project-scoped).
- `agents:` holds one inline hosted agent. `runtime: { stack: python }` + `startupCommand`
  selects **code-deploy**: `azd package` zips `src/basic-agent`, and `azd deploy` uploads it
  and creates the agent version.

## Run

```bash
azd env new            # create an environment
azd up                 # provision the Foundry project, then package + deploy the agent
```

To target an **existing** Foundry project (skip provision), add an `endpoint:` to the
service in `azure.yaml`:

```yaml
services:
  ai:
    host: microsoft.foundry
    endpoint: https://<account>.services.ai.azure.com/api/projects/<project>
```

## Notes

- This foundation supports a **single hosted agent** via **code-deploy (`runtime`)** or a
  prebuilt **`image`**. Container (`docker`) builds are declared faithfully by `init` but are
  not deployed by the service target yet.
- The `$schema` annotation points at the `huimiu/foundry-azure-yaml` integration branch while
  the feature is in preview; it flips back to `main` when the feature merges.
