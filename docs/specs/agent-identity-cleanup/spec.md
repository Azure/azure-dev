# Agent identity cleanup on `azd down`

<!-- cspell:ignore Entra -->

- **Issue:** Relates to [#10964](https://github.com/Azure/azure-dev/issues/10964)
- **Status:** Draft

## Goal

When `azd down` deletes a Foundry project, agent identities may remain. This
proposal defines which agents azd can delete and when, while protecting agents
that the environment does not own.

## Proposed behavior

- **Record ownership:** Record when the selected environment creates an agent.
  Deploying to an existing agent does not make it environment-owned. Treat
  older agents without ownership data as unowned.
- **Delete owned agents only with their project:** Before deleting the Foundry
  project, delete verified, environment-owned hosted, prompt, and voice agents
  through Foundry. Leave agents untouched when the project remains. This
  replaces prompt-agent cleanup that cannot verify ownership.
- **Honor teardown scope and consent:** Run cleanup only after the user
  confirms `azd down` or uses `--force`, and only when that operation deletes
  the Foundry project. Skip cleanup on cancellation, preview, or unrelated
  layer teardown. `--purge` alone is not consent.
- **Protect shared use:** Warn that deleting an agent removes all its versions
  and can end sessions used by other environments. Show the warning with
  `--force` too. For Digital Workers, skip cleanup if it is not safe to delete
  related objects.
- **Leave telephony bindings alone:** Do not delete bindings, since they may
  have existed before deployment. Warn users before confirmation and link to
  the [cleanup instructions](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/README.md#prompt-voice-telephony-bindings).
  With `--force`, show the warning without prompting.
- **Continue teardown on failure:** If agent cleanup fails, warn and continue
  teardown. Keep retry details while the Foundry project exists. Otherwise,
  include identity details in the warning.

## Dependencies to confirm

- **Foundry:** Confirm whether deleting each agent type removes its Entra
  identity, and confirm cleanup timing and required permissions. Do not claim
  this resolves [#10964](https://github.com/Azure/azure-dev/issues/10964)
  until identity removal is verified.
- **Digital Workers:** Confirm the effects on blueprints, other identities,
  and published Microsoft 365 users, and whether cleanup is safe when these
  objects are shared. See [#9615](https://github.com/Azure/azure-dev/issues/9615).
