# Agent identity cleanup on `azd down`

<!-- cspell:ignore Entra -->

- **Issue:** Relates to [#10964](https://github.com/Azure/azure-dev/issues/10964)
- **Status:** Draft (proposed behavior, not yet implemented)

## Goal

Deploying a Microsoft Foundry agent creates an identity in Microsoft Entra. Today, `azd down` can delete a Foundry project and leave the Entra identities for its hosted and voice agents behind. This proposal deletes environment-owned agents through Foundry during `azd down` before deleting the project, with the goal of removing their Entra identities without Microsoft Graph permissions.

## Proposed behavior

| Scenario | `azd down` behavior |
| --- | --- |
| This environment created the agent, and `azd down` will delete its Foundry project | Delete the agent through Foundry after confirmation (or `--force`), before deleting the project. |
| The agent already existed before this environment deployed to it | Skip agent cleanup and show a short note. |
| An older deployment has no ownership record | Skip agent cleanup and warn with manual cleanup options. |
| The Foundry project stays in place (such as a user-provided project) | Keep all agents and identities in that project, even ones this environment created. |
| Digital Worker cleanup cannot be confirmed safe for shared objects | Skip agent cleanup and warn. |
| Foundry agent delete fails | Warn and continue teardown. Keep retry data if the project stays, or print identity IDs if it is deleted. |
| Teardown is cancelled, previewed, or scoped to another layer | Leave agents and identities untouched. |

Skipping or failing agent cleanup does not block deleting a Foundry project that is in scope. Those agents stop working once the project is deleted, though their Entra identities may remain.

## Key rules and trade-offs

### Ownership on deploy

When `azd up` or `azd deploy` creates a hosted, prompt, or voice agent, `azd` records that the selected environment created it in that Foundry project. Later deploys can publish new versions without losing that ownership. Deploying to an existing agent still works, but `azd` does not claim ownership. Today, `azd down` deletes prompt agents without proof of creation; under this proposal, all three agent types need verified ownership.

### Consent and teardown order

Agent cleanup runs only when `azd down` will delete the Foundry project, after the user confirms deletion or passes `--force`, and before project deletion starts. Passing `--purge` alone does not skip confirmation or grant consent to delete resources.

### Shared versions, voice bindings, and purge

Deleting an agent removes all its versions and can end active sessions, including versions used by other environments. Because deleting the Foundry project affects those environments anyway, this proposal follows the project deletion boundary instead of adding a version check. `azd` warns about this impact before confirmation, and still prints the warning when `--force` skips the prompt.

Voice deployments can reuse existing telephony bindings, so `azd` does not delete bindings automatically. Before deleting a voice agent, `azd` warns before confirmation that bindings may remain and links to [telephony cleanup steps](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/README.md#prompt-voice-telephony-bindings). Users can cancel, remove bindings, and rerun `azd down`. With `--force`, `azd` prints the warning and continues, so users must clean up unwanted bindings first.

Microsoft Entra soft-deletes agent identities with a [30-day recovery window](https://learn.microsoft.com/en-us/entra/agent-id/concept-agent-identity-deletion) and does not state when deletion frees quota. Until Foundry confirms hard-delete support, `azd down --purge` will not promise permanent identity deletion or freed quota.

### Fallback when cleanup is skipped or fails

When ownership is unknown, `azd` tells users how to clean up manually. Before running `azd down`, users can run `azd ai agent delete` after checking that no other environment uses the agent. After project deletion, users can follow [Microsoft Entra cleanup guidance](https://learn.microsoft.com/en-us/entra/agent-id/howto-delete-agent-identity), which may need extra Entra permissions.

When Foundry fails to delete an owned agent, `azd` warns and continues teardown. If the Foundry project stays in place, `azd` keeps saved agent state so a retry can clean it up. If the project is deleted, the warning prints the identity client ID and principal ID so the user can find it in Entra.

## Service dependencies to confirm

- **Foundry:** Confirm whether deleting hosted, prompt, and voice agents removes their Entra identities, how long removal takes, what permissions it needs, and whether `--purge` can hard-delete them. Do not claim this fixes [#10964](https://github.com/Azure/azure-dev/issues/10964) until verified.
- **Digital Workers and Microsoft 365:** Confirm whether blueprints are shared across agents and what agent deletion does to other identities and published Microsoft 365 users ([#9615](https://github.com/Azure/azure-dev/issues/9615)).
