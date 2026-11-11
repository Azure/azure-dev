# Extension-owned workflows

This is the routing index for workflows maintained by the seven
`microsoft.foundry` extensions: `azure.ai.agents`, `azure.ai.connections`,
`azure.ai.inspector`, `azure.ai.projects`, `azure.ai.routines`, `azure.ai.skills`,
and `azure.ai.toolboxes`.

The root `azd-extensions` skill reads this index on invocation. Select only the
workflow relevant to the request; do not load or execute every workflow.
Repository and extension-local instructions still apply.

## Workflows

| Intent | Scope | Workflow | Execution boundary |
|---|---|---|---|
| Design a feature test plan; write or review unit, CLI/integration, or E2E tests | All seven extensions | [Feature test authoring](./azd-feature-test-authoring/workflow.md) | No live execution without explicit authorization |
| Write or fix cli-interactive-tester scenario YAML | Agents | [Scenario authoring](../azure.ai.agents/skills/foundry-extension-scenario-authoring/workflow.md) | Author and lint only; never run scenarios |
| Explicitly run scenarios affected by a PR or diff | Agents | [PR scenario regression](../azure.ai.agents/skills/foundry-extension-scenario-pr-regression/workflow.md) | Preserve prerequisite, scenario confirmation, cost, and cleanup gates |
| Explicitly run a full or tier/tag-filtered scenario sweep | Agents | [Scenario suite run](../azure.ai.agents/skills/foundry-extension-scenario-suite-run/workflow.md) | Preserve prerequisite, scenario confirmation, cost, and cleanup gates |

For general feature tests, follow feature test authoring. If the requested output
is a cli-interactive-tester YAML scenario, use scenario authoring for its format
and validation rules. It is not a live run. If the user asks only for a test plan,
do not implement or execute tests.

A coding request does not implicitly request a scenario sweep. Run workflows
require an explicit request; retain their consent and authorization gates.
The existing scenario agents remain available under their original names:

| Role | Instructions | Invocation |
|---|---|---|
| `foundry-extension-scenario-author` | [Scenario author](../azure.ai.agents/agents/foundry-extension-scenario-author.md) | User-selected; the authoring skill is also a model-triggered entry |
| `foundry-extension-scenario-orchestrator` | [Scenario orchestrator](../azure.ai.agents/agents/foundry-extension-scenario-orchestrator.md) | User-selected; coordinates a requested run |
| `foundry-extension-scenario-worker` | [Scenario worker](../azure.ai.agents/agents/foundry-extension-scenario-worker.md) | Explicitly assigned by an orchestrator/run skill; never auto-selected |

## Adding a workflow without changing root files

1. Add a focused workflow under this directory for shared guidance, or under the
   owning extension's `skills/<workflow-name>/` directory for local guidance.
   Store its instructions in `workflow.md` and supporting resources beside it.
2. Add a row to the Workflows table with its intent, extension scope, relative
   link, and execution boundary. Avoid overlapping routes; explain the distinction
   when two workflows are related.
3. Use explicit, relative links to resources and state when they must be read.
   Do not rely on template inclusion syntax or the caller's current directory.
4. Validate the links and entry points from the repository root:

   ```powershell
   python -m unittest discover -s cli\azd\extensions\skills\tests -v
   ```

5. In a fresh chat, invoke `/azd-extensions` with a representative request and
   confirm that it reads the index and selected workflow. Separately test
   description-based selection if automatic routing is required.

Only the root entry point is a discoverable skill. Index entries are
repository-local workflows, not independently registered skills or slash
commands. Adding an index entry does not automatically make it selectable by
name in a client's skill menu.

The existing scenario skills and custom agents keep thin root entry points for
compatibility. Their detailed instructions and references now live under
`azure.ai.agents`. Existing names and invocation restrictions remain unchanged.

New root commands, skill discovery metadata, custom-agent tools/invocation
metadata, CI execution, and permissions can still require root or platform
changes. This layout does not bypass ownership or review rules.

## Validation boundaries

The local tests check routing links, root compatibility entry points, invocation
restrictions, and registration of extension-local workflows. They do not prove
that a model selects or follows a workflow. Verify that separately with the
intended client; editor settings and discovery differ across clients.

No CI pipeline, live test, or Azure permission is changed by this routing layout.
