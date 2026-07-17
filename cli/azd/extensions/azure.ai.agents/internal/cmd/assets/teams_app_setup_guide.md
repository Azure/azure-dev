# Connect {{.AgentName}} to Microsoft Teams

`azd deploy` already did the Azure side for you:

- Azure Bot: `{{.BotName}}` (Microsoft Teams channel enabled)
- Bot ID (msaAppId): `{{.MsaAppID}}`  <- you will paste this as the bot id

Two manual steps remain: (A) create a Teams app package, then (B) upload it.
They are the same for any activity-protocol agent.

## Fastest path — run the generated script

`azd deploy` also wrote a runnable **pack-and-sideload script** next to this guide
that does A and B for you in one command (the Bot ID is already baked in):

```powershell
./pack-and-sideload-teams-app.ps1   # Windows / PowerShell
```
```sh
./pack-and-sideload-teams-app.sh    # macOS / Linux
```

It builds the Teams app package and installs it **for you** (`atk install --scope
Personal` — no org-catalog admin approval needed), then prints an "Open in Teams"
link. It is idempotent, so you can re-run it safely. Custom app upload
(sideloading) must be enabled for your tenant; if it is turned off, a Teams admin
must enable it (see the restricted-tenant note below).

Prerequisites:

- **Node.js** (for `npm`) — the script installs the Microsoft 365 Agents Toolkit
  CLI (`atk`) via npm if it is missing.
- A one-time **`atk auth login`** with your M365 account — the script launches
  this for you if you are not signed in.
- `--scope Personal` installs only for you and needs **no org-catalog admin
  approval** (an org-wide catalog upload does; see step B below). Custom app
  upload still has to be enabled for your tenant — if it is off, a Teams admin
  must turn it on (see the restricted-tenant note below).

Set `SKIP_TEAMS_INSTALL=1` to skip it. Prefer the manual / UI flow, a restricted
tenant, or custom manifest edits? Follow steps A and B below instead.

## A. Create the Teams app package

Pick ONE of the two ways below.

### Easiest — Teams Developer Portal (no files by hand)

1. Open https://dev.teams.microsoft.com/apps and select **+ New app**; enter a name.
2. Fill **Basic information** (short/long description, developer name and URLs).
3. Left menu **App features** -> **Bot** -> **Select an existing bot** -> enter the
   Bot ID `{{.MsaAppID}}`, tick the **Personal** scope, then **Save**.
4. **Publish** -> **Download the app package** — this gives you a ready-to-upload .zip.

Developer Portal guide: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/teams-developer-portal

### Or by hand — build the .zip yourself

Put these three files in a folder and zip them at the **root** (not inside a subfolder):

- `manifest.json` (below)
- `color.png`  — 192x192 px
- `outline.png` — 32x32 px, transparent background

```json
{
  "$schema": "https://developer.microsoft.com/json-schemas/teams/v1.19/MicrosoftTeams.schema.json",
  "manifestVersion": "1.19",
  "version": "1.0.0",
  "id": "REPLACE-WITH-A-NEW-GUID",
  "developer": {
    "name": "Your Company",
    "websiteUrl": "https://example.com",
    "privacyUrl": "https://example.com/privacy",
    "termsOfUseUrl": "https://example.com/terms"
  },
  "name": { "short": "{{.AgentName}}", "full": "{{.AgentName}}" },
  "description": { "short": "{{.AgentName}} agent", "full": "{{.AgentName}} agent on Microsoft Teams" },
  "icons": { "color": "color.png", "outline": "outline.png" },
  "accentColor": "#FFFFFF",
  "bots": [{ "botId": "{{.MsaAppID}}", "scopes": ["personal"] }]
}
```

Note: `id` is a NEW GUID for the app itself (generate one) — it is NOT the Bot ID.
Only `bots[].botId` uses the Bot ID above.

- Package + icon requirements: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/apps-package
- Manifest schema reference: https://learn.microsoft.com/microsoftteams/platform/resources/schema/manifest-schema
- Validate your .zip before uploading: https://dev.teams.microsoft.com/tools/store-validation

## B. Upload (sideload) the app — just for yourself

You do NOT need a Teams admin to try it yourself:

1. In Teams, go to **Apps** -> **Manage your apps** -> **Upload an app**.
2. Select **Upload a custom app**, choose your .zip, then **Add**.
3. Select **Open**, then send a message to talk to your agent.

Upload a custom app guide: https://learn.microsoft.com/microsoftteams/platform/concepts/deploy-and-publish/apps-upload

If **Upload a custom app** is missing or greyed out, custom app upload is turned off for
your tenant, or you want everyone in your org to get it from the org app catalog. Both need
a Teams admin: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/prepare-your-o365-tenant

## C. Optional — do both from the command line

Steps A and B can be scripted. This is a convenience path for repeat runs; it needs extra
tooling and does NOT bypass the tenant custom-app-upload setting above.

Package: put the manifest.json from section A (its Bot ID is already filled in) next to your
two icons, then zip the three files at the root:

```sh
zip -j {{.AgentName}}-teams-app.zip manifest.json color.png outline.png          # bash
```
```powershell
Compress-Archive manifest.json,color.png,outline.png {{.AgentName}}-teams-app.zip # PowerShell
```

Sideload for yourself with the Microsoft 365 Agents Toolkit CLI (atk). `--scope Personal` is a
per-user install and needs no org-catalog admin approval (custom app upload must still be
enabled for your tenant):

```sh
npm install -g @microsoft/m365agentstoolkit-cli          # one-time; requires Node.js
atk auth login                                           # sign in with your M365 account
atk install --file-path {{.AgentName}}-teams-app.zip --scope Personal
```

atk prints a TitleId and a Teams deep link you can open to launch the agent.
atk CLI reference: https://learn.microsoft.com/microsoftteams/platform/toolkit/microsoft-365-agents-toolkit-cli
