# Hooks

Azure Developer CLI hooks support multiple executor types — Bash, PowerShell,
Python (and future JavaScript, TypeScript, .NET). Every hook follows the same
unified lifecycle regardless of its executor: **Prepare → Execute → Cleanup**.

## Supported Executor Types

| Executor   | `language` value | File extension | Status       |
|------------|-----------------|----------------|--------------|
| Bash       | `sh`            | `.sh`          | ✅ Stable     |
| PowerShell | `pwsh`          | `.ps1`         | ✅ Stable     |
| Python     | `python`        | `.py`          | ✅ Phase 1    |
| JavaScript | `js`            | `.js`          | 🔜 Planned   |
| TypeScript | `ts`            | `.ts`          | 🔜 Planned   |
| .NET (C#)  | `dotnet`        | `.cs`          | 🔜 Planned   |

## Configuration

Hooks are configured in `azure.yaml` under the `hooks` section at the
project or service level. Two optional fields are available:

### `language` (string, optional)

Specifies the executor type for the hook. Allowed values:
`sh`, `pwsh`, `js`, `ts`, `python`, `dotnet`.

When omitted, the executor is **auto-detected** from the file extension of the
`run` path. For example, `run: ./hooks/seed.py` automatically selects the
Python executor.

### `dir` (string, optional) — working directory

The working directory (`cwd`) for hook execution. Used as the project context
for dependency installation (e.g. `pip install` from `requirements.txt`) and
builds.

**Automatically inferred** from the directory containing the script referenced
by `run`. For example, `run: hooks/preprovision/main.py` infers the working
directory as `hooks/preprovision/`. Only set `dir` as an override when the
project root differs from the script's directory (e.g. the entry point lives
in a `src/` subdirectory but `requirements.txt` is in the parent).

Relative paths are resolved from the project or service root.

## Examples

### Python hook — auto-detected from .py extension

The simplest way to use a Python hook. The executor is inferred from the `.py`
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
the `language` field explicitly to select the Python executor:

```yaml
hooks:
  postprovision:
    run: ./hooks/setup.py
    language: python
```

### Python hook with working directory override

When the script lives in a subdirectory but dependencies (`requirements.txt`)
are at the parent level, use `dir` to override the auto-inferred working
directory:

```yaml
hooks:
  postprovision:
    run: ./tools/scripts/seed.py
    dir: ./tools    # override: requirements.txt is in ./tools, not ./tools/scripts
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

Hooks support the `secrets` field for resolving Azure Key Vault references,
regardless of executor type:

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.py
    secrets:
      DB_CONNECTION_STRING: DATABASE_URL
```

### Bash hook (existing behavior, unchanged)

Bash hooks continue to work exactly as before. The `language` field is
optional and defaults to the appropriate shell type:

```yaml
hooks:
  preprovision:
    run: echo "Provisioning starting..."
    shell: sh
```

## How It Works

Every hook follows the unified **Prepare → Execute → Cleanup** lifecycle:

1. **Prepare** — The executor validates prerequisites and performs any
   setup. This includes:
   - **Language detection** from the explicit `language` field, the
     `shell` field, or the file extension of the `run` path.
   - **Runtime validation** — verifying the required runtime is
     installed (e.g. Python 3 for `.py` hooks, pwsh for `.ps1`).
   - **Project discovery** — walking up the directory tree from the
     script to find project files (`requirements.txt`, `pyproject.toml`,
     `package.json`, `*.*proj`). The search stops at the project/service
     root boundary.
   - **Dependency installation** — creating a virtual environment
     (for Python) and installing dependencies from the discovered
     project file.
   - **Temp file creation** — for inline scripts (Bash/PowerShell
     only), writing the script content to a temporary file.
2. **Execute** — The executor runs the hook using the appropriate
   runtime (e.g. `python`, `bash`, `pwsh`).
3. **Cleanup** — The executor removes any temporary resources created
   during Prepare (e.g. inline script temp files). This runs regardless
   of whether Execute succeeded or failed.

## Limitations

- **Inline scripts** are only supported for Bash and PowerShell hooks.
  All other executor types must reference a file path.
- **Phase 1** supports only Python as a non-shell executor. JavaScript,
  TypeScript, and .NET support is planned for future phases.
- **Virtual environments** are created in the project directory alongside the
  dependency file, following the naming convention `{dirName}_env`.
