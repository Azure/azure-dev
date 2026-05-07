# .azdignore - Template File Exclusion

## Overview

`.azdignore` lets template authors exclude files from being copied when consumers run `azd init`.
Place a `.azdignore` file at the root of your template repository to keep contributor-only files
(CI configs, internal docs, etc.) out of consumer projects.

## Syntax

`.azdignore` uses standard `.gitignore` pattern syntax (via [go-gitignore](https://github.com/denormal/go-gitignore)):

- `*` matches any characters within a path segment
- `**` matches across directory boundaries (recursive)
- `?` matches a single character
- `!pattern` negates a previously matched pattern
- `dirname/` matches a directory and all its contents
- Lines starting with `#` are comments
- Blank lines are ignored

## Where to Place It

Place `.azdignore` at the **root** of your template repository, alongside `azure.yaml`.

Only the root `.azdignore` is processed for ignore rules. Nested `.azdignore` files
(e.g., `docs/.azdignore`) are not processed but are still removed from consumer projects
to keep the output clean.

## Examples

A typical `.azdignore` for a template repository:

```gitignore
# CI/CD files - consumers don't need the template's CI config
.github/

# Contributor-only documentation
CONTRIBUTING.md
CODE_OF_CONDUCT.md
docs/internal/

# Build artifacts that shouldn't be in templates
**/node_modules
*.log
```

### Negation Example

Exclude all markdown files except `README.md`:

```gitignore
*.md
!README.md
```

## Behavior

- **Self-removing**: `.azdignore` is always removed from the consumer's project after
  processing. Consumers never see the `.azdignore` file.
- **Works with .gitignore**: When initializing from a local template, both `.gitignore`
  and `.azdignore` rules apply. `.azdignore` is preserved during the staging copy even if
  a `.gitignore` pattern would otherwise exclude it (e.g., `.*`).
- **No-op when absent**: If no `.azdignore` file exists, all template files are copied as usual.
- **Security**: Symlinked `.azdignore` files are rejected. Path traversal patterns (e.g., `../`)
  cannot escape the template directory. Files exceeding 1 MB are rejected.
