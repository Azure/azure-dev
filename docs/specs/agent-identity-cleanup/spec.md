# Agent identity cleanup on `azd down`

<!-- cspell:ignore Entra -->

- **Issue:** Relates to [#10964](https://github.com/Azure/azure-dev/issues/10964)
- **Status:** Draft

## Problem

Deploying a Microsoft Foundry agent creates an identity in Microsoft Entra. Today, `azd down` can delete the Foundry project without deleting the identities for its hosted and voice agents. These identities live outside the Azure resource group, so they can remain after teardown. With repeated `azd up` and `azd down` runs, they may count toward directory limits.

## Proposed approach

When a user runs `azd down`, `azd` should delete agents created by the selected environment through Foundry before it deletes the Foundry project. The goal is to remove their Entra identities too, without requiring `azd` to use Microsoft Graph or ask for extra Graph permissions.

To avoid deleting someone else's work, `azd` should only delete an agent when it can verify that the selected environment created that agent in that Foundry project. This rule applies to hosted, prompt, and voice agents. `azd` should skip agents that already existed when this environment deployed to them and older agents whose ownership it cannot verify. Today, `azd down` deletes prompt agents without proof that `azd` created them; the proposed rule changes that behavior.

`azd down` should not delete agents from a Foundry project it leaves in place, even if this environment created them. The Foundry provider already keeps user-provided projects; this proposal extends that boundary to agent cleanup. Their Entra identities may remain.

Deleting an agent removes all its versions and can end active sessions, including those used by other environments. Deleting the Foundry project can also affect those environments, even if `azd` skips agent cleanup. The proposal follows the existing project deletion scope rather than adding a separate version check. `azd` should warn about this impact before confirmation and also show the warning when `--force` skips confirmation.

Cleanup should run only after the user confirms deletion or the provider proceeds with `--force`. `--purge` alone does not grant consent to delete resources. Cleanup must not run if the user cancels, the operation only previews deletion, or a layer teardown does not delete the Foundry project.

This proposal does not automatically delete voice telephony bindings, since some may have existed before deploy. Before deleting a voice agent, `azd` should warn users before confirmation that bindings may remain and point them to [cleanup instructions](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.agents/README.md#prompt-voice-telephony-bindings). Users who want to remove bindings can cancel and clean them up before retrying. With `--force`, `azd` should still show the warning but continue without a prompt, so users must clean up bindings before the command if needed.

If agent cleanup fails, `azd` should warn the user but continue the rest of the teardown. When the Foundry project is still available, `azd` should keep the details needed for a retry. If the project is deleted, the warning should help the user find the identity in Entra.

This approach depends on the Foundry team confirming that deleting an agent also removes its Entra identity. For Digital Workers, the team must also confirm the effects on blueprints, other identities, and Microsoft 365 users. If `azd` cannot verify that cleanup is safe for shared objects, it should skip agent cleanup and warn. Until the service behavior is confirmed, identity cleanup is a goal, not a confirmed outcome.

| Situation | Proposed behavior |
| --- | --- |
| This environment created the agent and its project is being deleted | Delete the agent through Foundry before deleting the project, if cleanup is safe. |
| The agent already existed when this environment deployed | Skip agent and identity cleanup. Show a short note. |
| An older deployment has no proof of ownership | Skip agent and identity cleanup. Warn the user how to clean up. |
| The Foundry project is not being deleted | Leave its agents and their identities alone, including agents created by this environment. |
| Digital Worker cleanup cannot be verified as safe for shared objects | Skip agent cleanup and warn. |
| Agent cleanup fails | Continue teardown. Warn the user and keep retry details when possible. |
| Deletion is cancelled, previewed, or targets an unrelated layer | Leave agents and identities alone. |

Skipping agent cleanup does not stop `azd` from deleting a project in scope. Agents in that project may no longer be available, but their Entra identities may remain.

## Proposed user experience

### Deploy and redeploy

When `azd up` or `azd deploy` creates an agent, `azd` records that the environment created it. There is no extra prompt. A later deploy can add a version without losing that ownership.

If the agent already exists, `azd` can still deploy a new version, but it does not claim ownership:

```text
Agent "shared-agent" already exists; publishing a new version.
```

### Tear down an owned agent

After the user confirms `azd down`, `azd` deletes verified owned agents before deleting the Foundry project. It then continues the normal teardown:

```text
Deleting all resources and deployed code on Azure (azd down)
Local application code is not deleted when running 'azd down'.

WARNING: Deleting this Foundry project can affect agent versions used by other environments and end active sessions.
? Total resources to delete: 6. Are you sure you want to continue? Yes
Deleted agent "my-agent" from Foundry (Entra identity client ID: 00000000-0000-0000-0000-000000000001).
Deleting your resources can take some time.
...
Cleaned up saved agent context for agent "my-agent"
```

### Skip an existing agent

If the environment did not create an agent, `azd down` does not delete it through Foundry:

```text
Skipping agent "shared-agent": this azd environment did not create it.
```

### Warn when ownership is unknown

Older deployments may lack proof of ownership. `azd down` skips agent and identity cleanup and tells the user what to do:

```text
WARNING: Skipping cleanup for agent "my-agent" (Entra identity client ID: 00000000-0000-0000-0000-000000000001) because azd cannot verify that environment "dev" created it. The identity may remain after the Foundry project is deleted.
```

Users can delete an unverified agent with `azd ai agent delete` before running `azd down`. This deletes the whole agent, so they should first check whether others use it. After project deletion, they can follow [Microsoft Entra cleanup guidance](https://learn.microsoft.com/en-us/entra/agent-id/howto-delete-agent-identity); manual cleanup may need extra permissions.

### Warn when cleanup fails

If Foundry cannot delete an agent, `azd down` still tears down the Azure resources. It shows the identity details so the user can check for an orphaned identity:

```text
WARNING: Failed to delete owned agent "my-agent" from Foundry before infrastructure teardown: HTTP 403 Forbidden. Check Microsoft Entra for orphaned agent identity client ID "00000000-0000-0000-0000-000000000001" (principal ID "00000000-0000-0000-0000-000000000002").
```

## Purge and quota

Microsoft Entra documents a [30-day recovery period](https://learn.microsoft.com/en-us/entra/agent-id/concept-agent-identity-deletion) for soft-deleted agent identities, but does not specify when deletion frees quota. `azd down --purge` should not promise to permanently delete these identities or free quota until Foundry confirms that it supports this. The guidance should explain this limitation and link to Microsoft's manual cleanup steps.

## Dependencies to confirm

- **Foundry:** Confirm whether deleting each agent type removes its Entra identity, how long cleanup takes, and which permissions are needed. Do not claim this fixes [#10964](https://github.com/Azure/azure-dev/issues/10964) until identity removal is verified.
- **Digital Worker and Microsoft 365:** Confirm whether blueprints are shared and what happens to other identities and published Microsoft 365 users when an agent is deleted. [Microsoft Entra docs](https://learn.microsoft.com/en-us/entra/agent-id/concept-agent-identity-deletion) do not specify related-object cleanup. See [#9615](https://github.com/Azure/azure-dev/issues/9615).
- **Safe teardown:** Ensure cleanup happens after confirmation and only for the intended scope, before the Foundry project is deleted.
