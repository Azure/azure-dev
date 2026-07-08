## Reference: Source of Truth & Freshness

This skill is **static** — its embedded tables (capabilities, `azd x` flags, manifest fields) are a
**cached summary**, not a live feed. azd evolves (new capabilities, flags, SDK helpers), so the
snapshot **can drift**. To always recommend what is *actually* available, the agent MUST re-learn
from live sources at runtime and prefer them over the embedded summary whenever they disagree.

### Rule: verify against live sources before scaffolding or recommending

Treat the embedded content as a starting point only. Before you scaffold, choose capabilities, or
advise on flags/APIs, confirm against the ground truth for the **installed** azd version:

| Question | Live source to check (authoritative) |
|---|---|
| Which capabilities exist? | `azd x init --help` (dynamically lists valid capabilities) and `extension.schema.json` |
| Which `azd x` flags/commands exist? | `azd x --help`, `azd x <command> --help` |
| Correct manifest shape? | `cli/azd/extensions/extension.schema.json` (or its raw GitHub URL) |
| SDK helpers / patterns? | `extension-sdk-reference.md` (local or raw GitHub URL) |
| Real, working examples? | `cli/azd/extensions/microsoft.azd.demo` and other `azure.ai.*` extensions; `registry.json` |
| What's installed right now? | `azd extension list --installed`; an extension's `metadata` command |

CLI introspection (`--help`) and the JSON schema are the **most reliable** because they reflect the
exact installed version — always prefer them to the prose tables in this skill.

### Runtime freshness check the agent can run

```bash
# Ground-truth the capability list and flags for the installed azd/dev-extension version
azd x init --help          # shows the current valid --capabilities values
azd x --help               # shows the current azd x command surface
# Manifest schema (local first, else raw GitHub)
cat cli/azd/extensions/extension.schema.json 2>/dev/null \
  || curl -fsSL https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/extensions/extension.schema.json
```

If a live source lists something this skill doesn't mention (e.g., a new capability or flag), trust
the live source, use it, and note the discrepancy to the user.

### Portability note

When loaded **inside** `Azure/azure-dev`, the local `cli/azd/docs/extensions/*` files ARE the live
docs — read them directly. When loaded from **another** repo, fetch the raw GitHub URLs in
`overview-and-docs.md`. Either way, the docs are re-read at runtime, so they stay current without
editing this skill.

### Keeping the embedded summary in sync (maintainers)

The embedded tables mirror canonical sources and should be refreshed when those change:

- **Capabilities** ← `cli/azd/pkg/extensions/registry.go` (`CapabilityType` consts) and
  `validate_registry.go` (`ValidCapabilities`).
- **`azd x` flags/commands** ← `cli/azd/extensions/microsoft.azd.extensions/internal/cmd/*.go`.
- **Manifest fields** ← `cli/azd/extensions/extension.schema.json`.
- **Docs/links** ← `cli/azd/docs/extensions/*`.

When you refresh, bump `metadata.version` in `SKILL.md` (minor for additive updates). Consider a
lightweight CI reminder that flags edits to those canonical paths so the skill is reviewed for
sync. Because the skill also defers to live sources at runtime, a temporarily stale summary degrades
gracefully rather than producing wrong guidance — but keeping it synced improves first-pass quality.
