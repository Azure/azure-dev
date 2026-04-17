# Provision cancellation (Ctrl+C)

When `azd provision` (or `azd up`) submits a Bicep deployment to Azure, the
deployment runs asynchronously on the Azure side. If the user presses
<kbd>Ctrl</kbd>+<kbd>C</kbd> while azd is waiting for that deployment to
finish, azd will pause and ask what to do instead of exiting immediately.

## Behavior

1. azd stops the live progress reporter and presents an interactive prompt
   that includes the Azure portal URL of the running deployment.
2. The user picks one of:
   - **Leave the Azure deployment running and stop azd** (default). azd
     exits with a non-zero status; the Azure deployment continues to
     completion. The user can monitor or cancel it from the portal link.
   - **Cancel the Azure deployment**. azd submits an ARM cancel request
     against the deployment and waits up to 2 minutes for Azure to confirm a
     terminal state (`Canceled`, `Failed`, or `Succeeded`).
3. Additional <kbd>Ctrl</kbd>+<kbd>C</kbd> presses while the prompt is
   showing (or while a cancel request is in flight) are ignored so the user
   can finish reading and choose deliberately.

## Outcomes when "Cancel" is selected

| Outcome | When |
|---------|------|
| Cancellation confirmed | Azure transitions the deployment to `Canceled` within the wait budget. azd exits non-zero with a clear message. |
| Cancel arrived too late | Azure reports the deployment finished (`Succeeded` / `Failed`) before the cancel request took effect. azd surfaces the final state plus the portal URL. |
| Cancel still pending | Azure does not reach a terminal state within the wait budget. azd warns that cancellation is still in progress and prints the portal URL. |
| Cancel request failed | The ARM `Cancel` API itself returned an error. azd prints the error and the portal URL. |

When the deployment URL is available, azd prints it so the user can follow
up manually from the browser. The URL is omitted if azd was unable to
resolve it (for example, when the ARM service is unreachable).

## Provider scope

| Provider | Behavior on Ctrl+C during provision |
|---------|--------------------------------------|
| Bicep (subscription scope) | Interactive prompt (described above). |
| Bicep (resource group scope) | Interactive prompt (described above). |
| Deployment Stacks | Currently treated as "leave running" — the stacks ARM API does not expose a per-deployment cancel surface today. |
| Terraform | Unchanged: the Terraform CLI does not expose a safe per-apply cancel; pressing Ctrl+C exits azd and Terraform handles its own teardown. |

## Telemetry

A `provision.cancellation` attribute is recorded on the provisioning span
with one of:

- `none` — provisioning completed normally without an interrupt.
- `leave_running` — user chose to let the Azure deployment continue.
- `canceled` — cancel request succeeded and Azure reached `Canceled`.
- `cancel_too_late` — Azure reached `Succeeded` / `Failed` before cancel
  took effect.
- `cancel_timed_out` — Azure did not reach a terminal state within the
  wait budget.
- `cancel_failed` — the ARM `Cancel` API call itself returned an error.

## Non-interactive mode

If azd is running without a TTY (e.g. CI), the prompt cannot be displayed.
In that case azd defaults to **leave running** behavior so that an
unattended deployment is never silently cancelled by an environment
signal.
