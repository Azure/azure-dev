# Foundry `azure.yaml` schemas

These schemas describe Foundry resource shapes used by `azure.yaml`.

The root `azure.yaml` schemas in `schemas/v1.0/azure.yaml.json` and
`schemas/alpha/azure.yaml.json` are consumed from the repository's `main` raw
URLs through SchemaStore and generated `# yaml-language-server: $schema=...`
comments. Keep the `$id` values and example `$schema` annotations in this
directory on `main` as well, so editor validators can resolve composed schemas
after release.

Relative `$ref`s, for example `"FileRef.json"`, resolve against the parent
schema's `$id`.

## Local validation

Validate the examples with `ajv`, loading local schema files by their `$id` so no
network fetch is needed:

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

The example file-ref targets such as `./agents/triage.yaml` are illustrative;
they are validated as `FileRef` shapes, so the referenced files do not need to
exist for schema validation.
