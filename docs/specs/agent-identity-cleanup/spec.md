# Agent identity cleanup on `azd down`

<!-- cspell:ignore Entra -->

- **Issue:** Relates to [#10964](https://github.com/Azure/azure-dev/issues/10964)
- **Status:** Draft

## Summary

`azd down` can leave agent identities in Microsoft Entra after deleting a
Foundry project. This proposal would clean up eligible agents through Foundry
before project deletion, with the goal of removing their identities without
extra Microsoft Graph permissions. Identity removal still needs confirmation
from Foundry. The behavior below is proposed, not yet implemented.

## Changes

### Track agent ownership

When `azd up` or `azd deploy` creates an agent, azd records that the selected
environment created it in that Foundry project. Later deployments can add
versions without losing that ownership.

Users can still deploy to an existing agent, but azd does not claim ownership
of it. For older deployments without proof of ownership, azd treats ownership
as unknown and skips explicit agent cleanup.

### Clean up agents during teardown

When `azd down` will delete a Foundry project, azd deletes hosted, prompt, and
voice agents through Foundry only if it can verify that the selected
environment created them in that project. Cleanup runs after the user confirms
deletion, or the provider proceeds with `--force`, and before project
deletion. This replaces the current prompt-agent cleanup that does not verify
ownership.

If the project stays, azd leaves its agents alone, including agents created by
the environment. This includes user-provided projects that the Foundry
provider keeps. Cleanup also does not run if deletion is cancelled, only
previewed, or scoped to a layer that does not delete the project. `--purge`
alone does not grant consent to delete resources.

For Digital Workers, azd skips explicit agent cleanup and warns if it cannot
verify that deleting related shared objects is safe.

Skipping explicit agent cleanup does not stop deletion of a project in scope.
Its agents may no longer be available after project deletion, and their Entra
identities may remain.

## User-facing changes

Before confirmation, azd warns that deleting an agent removes all its versions
and can end active sessions, including those used by other environments.
Deleting the project can also affect those environments even if azd skips
explicit agent cleanup. With `--force`, azd still shows the warning but does
not prompt.

For voice agents, azd does not delete telephony bindings because they may have
existed before deployment. It warns before confirmation that bindings may
remain and links to the
[cleanup instructions](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/README.md#prompt-voice-telephony-bindings).
Users can cancel and remove bindings before retrying. With `--force`, azd
warns but continues, so users must remove unwanted bindings beforehand.

When azd skips an existing agent, it shows a short note. When ownership is
unknown, it warns that the identity may remain and gives users details to find
and clean it up. Users can delete the agent before teardown or follow the
[Entra cleanup guidance](https://learn.microsoft.com/en-us/entra/agent-id/howto-delete-agent-identity)
after project deletion.

If agent cleanup fails, azd warns but continues the rest of the teardown. It
keeps the details needed for a retry while the project remains available. If
the project is deleted, the warning includes identity details for manual
cleanup.

`azd down --purge` should not promise to permanently delete agent identities
or free quota until Foundry confirms that it supports this.

## Dependencies to confirm

- **Foundry:** Confirm whether deleting each agent type removes its Entra
  identity, and confirm cleanup timing and required permissions. Do not claim
  this resolves [#10964](https://github.com/Azure/azure-dev/issues/10964)
  until identity removal is verified.
- **Digital Workers:** Confirm the effects on blueprints, other identities,
  and published Microsoft 365 users, and whether cleanup is safe when these
  objects are shared. See [#9615](https://github.com/Azure/azure-dev/issues/9615).
