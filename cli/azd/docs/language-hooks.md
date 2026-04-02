# Language Hooks

Azure Developer CLI supports running hook scripts in programming languages beyond
shell scripts (Bash/PowerShell). Language hooks use a language-specific executor that
automatically handles dependency installation and runtime management.

## Supported Languages

| Language   | `language` value | File extension | Status       |
|------------|-----------------|----------------|--------------|
| Bash       | `sh`            | `.sh`          | ✅ Stable     |
| PowerShell | `pwsh`          | `.ps1`         | ✅ Stable     |
| Python     | `python`        | `.py`          | ✅ Phase 1    |
| JavaScript | `js`            | `.js`          | 🔜 Planned   |
| TypeScript | `ts`            | `.ts`          | 🔜 Planned   |
| .NET (C#)  | `dotnet`        | `.cs`          | 🔜 Planned   |

## Configuration

Language hooks are configured in `azure.yaml` under the `hooks` section at the
project or service level. Two new optional fields are available:

### `language` (string, optional)

Specifies the programming language of the hook script. Allowed values:
`sh`, `pwsh`, `js`, `ts`, `python`, `dotnet`.

When omitted, the language is **auto-detected** from the file extension of the
`run` path. For example, `run: ./hooks/seed.py` automatically sets
`language: python`.

### `dir` (string, optional)

Specifies the working directory for language hook execution, used as the project
context for dependency installation (e.g. `pip install` from `requirements.txt`)
and builds.

When omitted, **automatically inferred** from the directory containing the script
referenced by `run`. For example, `run: hooks/preprovision/main.py` sets the
working directory to `hooks/preprovision/`. Only set `dir` when the project root
differs from the script's directory (e.g. when the entry point lives in a `src/`
subdirectory).

Relative paths are resolved from the project or service root.

## Examples

### Python hook — auto-detected from .py extension

The simplest way to use a Python hook. The language is inferred from the `.py`
extension, and the working directory is auto-inferred from the script's location.
Dependencies are installed automatically if a `requirements.txt` or
`pyproject.toml` is found in the script's directory.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.py
```

### Python hook in a subdirectory (dir auto-inferred)

When the script lives in a subdirectory, the `dir` is automatically set to that
directory. No explicit `dir` field is needed:

```yaml
hooks:
  preprovision:
    run: hooks/preprovision/main.py
    # dir is auto-inferred as hooks/preprovision/
```

### Python hook — explicit language

When auto-detection is not desired or the file extension is ambiguous, set
the `language` field explicitly:

```yaml
hooks:
  postprovision:
    run: ./hooks/setup.py
    language: python
```

### Python hook with project directory override

When the script's project root differs from the script's directory (e.g. the
entry point is in a `src/` subdirectory but dependencies are at the project
level), use `dir` to override the auto-inferred value:

```yaml
hooks:
  postprovision:
    run: ./hooks/data-tool/src/main.py
    language: python
    dir: ./hooks/data-tool    # override: project root differs from script location
```

### Python hook with platform overrides

Use `windows` and `posix` overrides to provide platform-specific hooks:

```yaml
hooks:
  postprovision:
    windows:
      run: ./hooks/setup.ps1
      shell: pwsh
    posix:
      run: ./hooks/setup.py
      language: python
```

### Python hook with secrets

Language hooks support the same `secrets` field as shell hooks for
resolving Azure Key Vault references:

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.py
    secrets:
      DB_CONNECTION_STRING: DATABASE_URL
```

### Shell hook (existing behavior, unchanged)

Shell hooks continue to work exactly as before. The `language` field is
optional and defaults to the shell type:

```yaml
hooks:
  preprovision:
    run: echo "Provisioning starting..."
    shell: sh
```

## How It Works

When a language hook runs, the executor performs these steps:

1. **Language Detection** — Determines the script language from the explicit
   `language` field, the `shell` field, or the file extension.
2. **Runtime Validation** — Verifies the required runtime is installed
   (e.g. Python 3 for `.py` hooks).
3. **Project Discovery** — Walks up the directory tree from the script to
   find project files (`requirements.txt`, `pyproject.toml`, `package.json`,
   `*.*proj`). The search stops at the project/service root boundary.
4. **Dependency Installation** — Creates a virtual environment (for Python)
   and installs dependencies from the discovered project file.
5. **Script Execution** — Runs the script with the language runtime, using
   the virtual environment if one was created.

## Limitations

- **Inline scripts** are only supported for shell hooks (`sh`, `pwsh`).
  Language hooks must reference a file path.
- **Phase 1** supports only Python. JavaScript, TypeScript, and .NET support
  is planned for future phases.
- **Virtual environments** are created in the project directory alongside the
  dependency file, following the naming convention `{dirName}_env`.
