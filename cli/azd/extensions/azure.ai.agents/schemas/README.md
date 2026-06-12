# Foundry `azure.yaml` schemas (preview — integration branch)

These schemas model `host: microsoft.foundry` in `azure.yaml`. They are **not on `main`**.
They live on the long-lived integration branch **`huimiu/foundry-azure-yaml`** so the feature can
be prototyped and tested without being published to every `azd` user before it is ready.

## Why the URLs point at the branch

The core `azure.yaml` schema is consumed *live* from `main`:

- `schemas/schemastore-catalog-entry.json` registers it with **SchemaStore** at the `main` raw URL.
- `azd` stamps `# yaml-language-server: $schema=…/main/schemas/<ver>/azure.yaml.json` into generated
  `azure.yaml` files.

So merging `microsoft.foundry` into `main` would immediately surface an unfinished feature in every
editor. To test on the branch instead, the **new Foundry** schema URLs are rewritten from the `main`
raw URL to the `huimiu/foundry-azure-yaml` branch. All schema URLs use the short
`raw.githubusercontent.com/Azure/azure-dev/<ref>/…` form (no `refs/heads/` segment), matching the
core schema's own root `$id` and the SchemaStore retrieval URL:

- the `$id` of every file in this directory (`Agent.json`, `Skill.json`, `Routine.json`,
  `Connection.json`, `Toolbox.json`, `Deployment.json`, `FileRef.json`, `microsoft.foundry.json`);
- the `microsoft.foundry.json` `$ref` added to `schemas/v1.0/azure.yaml.json` and
  `schemas/alpha/azure.yaml.json`;
- the `$schema` annotation in `examples/*.azure.yaml`.

Relative `$ref`s (for example `"FileRef.json"`) resolve against the parent's `$id`, so they follow
the branch automatically.

**Deliberately left pointing at `main`** (published / shared surfaces, unchanged by this work):

- the core schema's own root `$id`;
- the pre-existing `azure.ai.agent.json` `$ref` in the core schema (still `main`; only its URL form
  was normalized to the short form for consistency);
- `schemas/schemastore-catalog-entry.json`;
- `cli/azd/pkg/project/project.go` and `resources/apphost/templates/azure.yamlt`.

## How to test on the branch

1. Push the integration branch to `origin` (the raw URLs only resolve once the branch is published).
2. **In an editor (primary):** open `examples/simple.azure.yaml` or `examples/complex.azure.yaml`.
   The `$schema` comment makes the YAML language server validate the file against the branch schema,
   resolving the composed Foundry sub-schemas over the branch raw URLs.
3. **CLI (optional, offline):** validate the samples with `ajv`, loading the local schema files by
   their `$id` so no network fetch is needed:

   ```bash
   # from the repo root, in a scratch dir
   npm init -y && npm install ajv@8 ajv-formats js-yaml glob
   node - <<'EOF'
   const fs = require('fs');
   const path = require('path');
   const yaml = require('js-yaml');
   const Ajv = require('ajv/dist/2019').default;
   const addFormats = require('ajv-formats').default;
   const { globSync } = require('glob');

   const repo = process.env.REPO || '.';
   const ajv = new Ajv({ allErrors: true, strict: false });
   addFormats(ajv);
   ajv.addMetaSchema(require('ajv/dist/refs/json-schema-draft-07.json'));

   // Register every schema under its own $id (core + Foundry sub-schemas + existing agent schema).
   // The pre-existing azure.ai.agent.json has no $id, so register it under the `main` raw URL the
   // core schema references.
   const core = path.join(repo, 'schemas/v1.0/azure.yaml.json');
   const subs = globSync(path.join(repo, 'cli/azd/extensions/azure.ai.agents/schemas/*.json'));
   for (const f of [core, ...subs]) {
     const s = JSON.parse(fs.readFileSync(f, 'utf8'));
     const rel = path.relative(repo, f).replace(/\\/g, '/');
     const id = s.$id || `https://raw.githubusercontent.com/Azure/azure-dev/main/${rel}`;
     ajv.addSchema(s, id);
   }

   const validate = ajv.getSchema(JSON.parse(fs.readFileSync(core, 'utf8')).$id);
   for (const s of globSync(path.join(repo,
       'cli/azd/extensions/azure.ai.agents/schemas/examples/*.azure.yaml'))) {
     const ok = validate(yaml.load(fs.readFileSync(s, 'utf8')));
     console.log(`${ok ? 'PASS' : 'FAIL'}  ${path.basename(s)}`);
     if (!ok) { console.error(validate.errors); process.exitCode = 1; }
   }
   EOF
   ```

   The example file-ref targets (`./agents/triage.yaml`, etc.) are illustrative; they are validated as
   `FileRef` shapes, so the referenced files do not need to exist for schema validation.

## Before merging to `main` — flip-back checklist (required)

When the prototype is ready and the integration branch is merged into `main`, rewrite the branch URLs
back so the published schema is self-consistent on `main`:

- [ ] In all files in this directory, change `$id` `huimiu/foundry-azure-yaml` → `main`.
- [ ] In `schemas/v1.0/azure.yaml.json` and `schemas/alpha/azure.yaml.json`, change the
      `microsoft.foundry.json` `$ref` `huimiu/foundry-azure-yaml` → `main`.
- [ ] In `examples/*.azure.yaml`, change the `$schema` annotation
      `huimiu/foundry-azure-yaml` → `main`.
- [ ] Confirm no `huimiu/foundry-azure-yaml` references remain:
      `git grep -n "huimiu/foundry-azure-yaml"` returns nothing.
- [ ] Re-validate the samples (steps above) against the `main` URLs.
