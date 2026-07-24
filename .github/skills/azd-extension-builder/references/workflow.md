## Reference: End-to-End Workflow (agent decision guide)

Follow this loop. Only do the phase(s) the user asked for; don't over-reach.

### Step 0 — Locate the canonical docs

- If inside the `Azure/azure-dev` repo, read `cli/azd/docs/extensions/*` locally.
- Otherwise fetch the raw GitHub URLs (see `overview-and-docs.md`). Never assume the source tree is
  present — this skill is portable.
- **Re-learn from live sources**: this skill's embedded tables are a cached summary and may be
  stale. Before scaffolding or recommending, verify capabilities/flags/manifest against the live
  ground truth (`azd x --help`, `azd x init --help`, `extension.schema.json`) and prefer them when
  they disagree. See `source-of-truth-and-freshness.md`.

### Step 1 — Clarify intent (ask if unclear)

Ask via `ask_user` when ambiguous:

1. **Audience**: first-party (inside azure-dev) or external (own repo)?
2. **Language**: go / dotnet / javascript / python? (first-party = Go only)
3. **Capabilities**: which of the 8? (custom-commands, lifecycle-events, mcp-server,
   service-target-provider, framework-service-provider, provisioning-provider, validation-provider,
   metadata)
4. **Phase**: scaffold only, or through build/test/release/publish?

### Step 2 — Verify prerequisites (auto-install `azd x`)

```bash
azd version >/dev/null 2>&1 || { echo "install azd first"; }
azd x version >/dev/null 2>&1 || azd extension install microsoft.azd.extensions
azd x version
```

The developer extension is in the **pre-configured official registry**, so this installs with no
source setup and no alpha flag. If `azd` itself is missing, install it first
(`prerequisites-and-setup.md`) — surface the command rather than installing silently. For
release/publish, confirm `gh auth status`.

### Step 3 — Scaffold

- External: `azd x init` (from project root).
- First-party: `cd cli/azd/extensions && azd x init --internal` (requires `--codeowners`).
- Prefer interactive unless the user wants headless; for headless supply the required flags.
- After scaffolding, review/edit `extension.yaml` so `id`, `namespace`, `displayName`,
  `description`, `version`, `capabilities` (and `providers`/`mcp`/`examples` if applicable) are
  correct. Validate against `extension.schema.json`.

### Step 4 — Implement capabilities

Wire each declared capability with the `azdext` SDK (`capabilities-and-sdk.md`). Model complex
providers after `microsoft.azd.demo`. Keep manifest capabilities in sync with the code. Follow the
extension style guide (reserved flags, structured errors, `fmt` vs `log`).

### Step 5 — Build, install, test (iterate)

```bash
azd x build            # build + install current platform
azd <namespace> --help # verify wiring
azd x watch            # optional fast loop while developing
```

Run unit tests; for first-party, respect Go conventions (no `os.Stdout` in tests, ≤125 char lines)
and update snapshot tests if `registry.json` changed. Iterate until green.

### Step 6 — Release & publish (when requested)

- **External**: `azd x build --all` → `azd x pack --rebuild` → `azd x release --repo owner/repo` →
  `azd x publish --repo owner/repo`. Requires GitHub auth.
- **First-party**: PR 1 (bump `version.txt` + `extension.yaml` + `CHANGELOG.md`) → CI release →
  PR 2 (regenerate the `registry.json` entry via `azd x publish`). Do not release directly against
  `Azure/azure-dev`.

### Step 7 — Summarize

Report what was created/changed, how to install/run it, and the exact next command. Confirm the
manifest validates and capabilities match the implementation.

### Guardrails

- Don't fabricate capabilities/flags. The embedded lists are a cached summary — **verify against
  live sources** (`azd x --help`, `extension.schema.json`, canonical docs) and prefer them when they
  disagree (`source-of-truth-and-freshness.md`).
- Don't run `azd x release`/`publish` against `Azure/azure-dev` (use the first-party 2-PR flow).
- Don't add reserved global flags to extension commands.
- Ask before destructive actions (publishing, creating GitHub releases, overwriting a manifest).
- Keep `extension.yaml` capabilities and `providers` consistent with the implemented code.
