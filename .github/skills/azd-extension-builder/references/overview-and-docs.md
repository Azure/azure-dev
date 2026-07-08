## Reference: Overview & Documentation Map

### What an azd extension is

An `azd` extension is a standalone executable that `azd` discovers and runs. Communication with
`azd` happens over a gRPC framework (the `azdext` SDK wraps it). Extensions are distributed through
**extension sources** (file- or URL-based registries), installed with `azd extension install`, and
invoked under their **namespace** (e.g. namespace `tagger` → `azd tagger ...`; a dotted namespace
`ai.project` becomes nested commands `azd ai project ...`).

Supported implementation languages: **Go, .NET, JavaScript, Python** (Go is the reference language
with the richest SDK helpers).

### Canonical documentation (authoritative source of truth)

These live in the `Azure/azure-dev` repo under `cli/azd/docs/extensions/`. When this skill is
loaded **inside** that repo, read the local files. When loaded from **another** repo, fetch the raw
URLs below.

| Topic | Local path (`cli/azd/docs/extensions/`) | Raw URL |
|---|---|---|
| Framework (overview, sources, capabilities, structure, publishing) | `extension-framework.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-framework.md` |
| SDK reference (azdext helpers) | `extension-sdk-reference.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-sdk-reference.md` |
| End-to-end walkthrough (build a real extension) | `extension-e2e-walkthrough.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-e2e-walkthrough.md` |
| Framework-service (custom language) providers | `extension-framework-services.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-framework-services.md` |
| Resolution & versioning (sources, constraints, bundles, nightly/dev) | `extension-resolution-and-versioning.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-resolution-and-versioning.md` |
| Migration guide (legacy → SDK helpers) | `extension-migration-guide.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-migration-guide.md` |
| Extension style guide (design, flags, errors) | `extensions-style-guide.md` | `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extensions-style-guide.md` |

Related schemas and registries:

- Extension manifest schema: `cli/azd/extensions/extension.schema.json`
  (raw: `https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/extensions/extension.schema.json`)
- Registry schema: `cli/azd/extensions/registry.schema.json`
- Official registry (`registry.json`): `https://aka.ms/azd/extensions/registry`
- Dev/experimental registry (`registry.dev.json`): `https://aka.ms/azd/extensions/registry/dev`
- Developer extension source: `cli/azd/extensions/microsoft.azd.extensions/`

> **Portability rule for the agent**: Always try the local doc path first (`view`/`grep`). If it
> does not exist (skill referenced from another repo), fetch the raw URL. Never assume the
> azure-dev source tree is present.

### How to fetch canonical docs when outside the repo

Use a web fetch tool on the raw URL, or:

```bash
curl -fsSL https://raw.githubusercontent.com/Azure/azure-dev/main/cli/azd/docs/extensions/extension-sdk-reference.md
```

### First-party reference extensions (read these for real examples)

Inside the azure-dev repo, `cli/azd/extensions/` contains working extensions to model after:

- `microsoft.azd.demo` — demonstrates every capability (custom-commands, lifecycle-events,
  mcp-server, service-target-provider, framework-service-provider, provisioning-provider,
  validation-provider, metadata). The best single reference.
- `microsoft.azd.extensions` — the developer extension itself (`azd x`).
- `azure.ai.*` extensions — production first-party extensions, each with an `AGENTS.md`
  documenting its conventions.
