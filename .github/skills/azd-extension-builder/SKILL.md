---
name: azd-extension-builder
license: MIT
metadata:
  version: "1.0"
  # Bump major on breaking prompt/trigger changes; minor on new references or workflow additions.
description: >-
  **WORKFLOW SKILL** — Expert guide for building Azure Developer CLI (azd) extensions end to end:
  scaffold, develop, build, install, test, release, and publish. Covers both first-party extensions
  inside the Azure/azure-dev repo (`azd x init --internal`) and external extensions in any repo.
  Portable: works when referenced from any repository or path.

  INVOKES: azd CLI, azd x (microsoft.azd.extensions developer extension), language toolchains
  (go/dotnet/node/python), gh CLI, git CLI.

  USE FOR: create/scaffold/build/test/release/publish azd extension, azd x init, azd x build,
  extension.yaml manifest, azd extension capabilities (mcp-server, lifecycle-events,
  service-target-provider), first-party azd extension, azdext SDK, azd extension registry.

  DO NOT USE FOR: authoring core azd commands under cli/azd/cmd (not extensions), azd preflight
  (use azd-preflight), changelogs (use changelog-generation), PR review (use azd-code-reviewer).
---

# azd-extension-builder

Make an agent an expert at building Azure Developer CLI (`azd`) extensions — from an empty
directory to a published, installable extension.

## Overview

`azd` extensions are standalone executables (Go, .NET, JavaScript, or Python) that plug into `azd`
over gRPC — adding custom commands, lifecycle hooks, MCP tools, and service-target / framework /
provisioning / validation providers. The `microsoft.azd.extensions` developer extension supplies the
`azd x` command suite (init, build, watch, pack, release, publish).

Two audiences:

1. **First-party** — contributing **inside** `Azure/azure-dev` under `cli/azd/extensions/`
   (`azd x init --internal`, Go-only, 2-PR release flow).
2. **External** — building in **their own repo** (`azd x init`, then `azd x release` + `azd x publish`).

This skill is **portable**: its reference files embed the essential knowledge inline and link to
canonical GitHub docs, so it works even when loaded from a repo without the azure-dev source tree.

> **Freshness:** the embedded content is a **cached summary** that can drift as azd evolves.
> Verify against live sources at runtime (`azd x --help`, `extension.schema.json`, the docs) and
> prefer the live source on conflict. See `references/source-of-truth-and-freshness.md`.

## How to work

1. **Determine audience** (first-party vs external) and target **language** — ask if unclear.
2. **Ensure prerequisites** — verify `azd`, then **auto-install the developer extension if missing**
   (`azd x version || azd extension install microsoft.azd.extensions`; it ships in the pre-configured
   official registry). Add the language toolchain, and `gh` for release/publish.
3. **Scaffold** with `azd x init` (`--internal` for first-party), edit `extension.yaml`, and wire
   capabilities via the `azdext` SDK.
4. **Build, install, test** iteratively with `azd x build` / `azd x watch`.
5. **Release and publish** using the correct flow for the audience.

Follow the decision-driven walkthrough in `references/workflow.md`. Each step's detail lives in the
reference files below.

{{ references/overview-and-docs.md }}

{{ references/source-of-truth-and-freshness.md }}

{{ references/prerequisites-and-setup.md }}

{{ references/scaffolding.md }}

{{ references/capabilities-and-sdk.md }}

{{ references/build-test-install.md }}

{{ references/release-and-publish.md }}

{{ references/workflow.md }}

## Exit criteria

- The requested phase is complete and verified: **scaffold** (`azd x build` succeeds; installed
  extension responds to `azd <namespace> --help`), **develop** (new commands/tools/handlers build
  and run), or **release/publish** (GitHub release exists or first-party version-bump PR is prepared
  and the target registry entry is generated).
- `extension.yaml` validates against the schema and capabilities match the code.
- The correct audience flow (first-party vs external) was used.
- A concise summary of changes and the next command to run is shown.
