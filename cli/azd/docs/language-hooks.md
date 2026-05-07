# Hooks

Azure Developer CLI hooks support multiple executor types — Bash, PowerShell,
Python (and future JavaScript, TypeScript, .NET). Every hook follows the same
unified lifecycle regardless of its executor: **Prepare → Execute → Cleanup**.

## Supported Executor Types

| Executor   | `kind` value | File extension | Status       |
|------------|-------------|----------------|--------------|
| Bash       | `sh`        | `.sh`          | ✅ Stable     |
| PowerShell | `pwsh`      | `.ps1`         | ✅ Stable     |
| Python     | `python`    | `.py`          | ✅ Phase 1    |
| JavaScript | `js`        | `.js`          | ✅ Phase 2    |
| TypeScript | `ts`        | `.ts`          | ✅ Phase 3    |
| .NET (C#)  | `dotnet`    | `.cs`          | ✅ Phase 4    |

## Configuration

Hooks are configured in `azure.yaml` under the `hooks` section at the
project or service level. Two optional fields are available:

### `kind` (string, optional)

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

### Python hook — explicit kind

When auto-detection is not desired or the file extension is ambiguous, set
the `kind` field explicitly to select the Python executor:

```yaml
hooks:
  postprovision:
    run: ./hooks/setup.py
    kind: python
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
      kind: python
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

### JavaScript hook — auto-detected from .js extension

The simplest way to use a JavaScript hook. The executor is inferred from the `.js`
extension. Dependencies are installed automatically if a `package.json` is found
in the script's directory (or a parent directory up to the project root).

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.js
```

### JavaScript hook with package.json

When a `package.json` exists near the script, `npm install` runs automatically
before execution.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.js
    # package.json in ./hooks/ → npm install runs automatically
```

### JavaScript hook — explicit kind

```yaml
hooks:
  postprovision:
    run: ./hooks/setup
    kind: js
```

### JavaScript hook with working directory override

```yaml
hooks:
  postprovision:
    run: ./tools/scripts/seed.js
    dir: ./tools    # package.json is in ./tools, not ./tools/scripts
```

### JavaScript hook with platform overrides

```yaml
hooks:
  postprovision:
    windows:
      run: ./hooks/setup.ps1
      shell: pwsh
    posix:
      run: ./hooks/setup.js
      kind: js
```

### TypeScript hook — auto-detected from .ts extension

TypeScript hooks use `npx tsx` for zero-config execution. `tsx` handles
TypeScript natively without requiring a separate compilation step, and
supports both ESM and CommonJS modules automatically.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.ts
```

### TypeScript hook with package.json

When a `package.json` is found, dependencies are installed before execution.
If `tsx` is listed as a dependency, the local version is used; otherwise
`npx` downloads it on demand.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.ts
    # package.json with tsx dependency → uses local tsx
```

### TypeScript hook — explicit kind

```yaml
hooks:
  postprovision:
    run: ./hooks/setup
    kind: ts
```

### Bash hook (existing behavior, unchanged)

Bash hooks continue to work exactly as before. The `kind` field is
optional and defaults to the appropriate shell type:

```yaml
hooks:
  preprovision:
    run: echo "Provisioning starting..."
    shell: sh
```

### .NET hook with project — auto-detected from .cs extension

When a `.csproj` (or `.fsproj`/`.vbproj`) is found near the script, azd
automatically runs `dotnet restore` and `dotnet build` during preparation,
then executes via `dotnet run --project`.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.cs
    # .csproj in ./hooks/ → restore + build run automatically
```

### .NET single-file hook (.NET 10+)

On .NET 10 or later, single `.cs` files can run without a project file.
azd detects the SDK version and runs `dotnet run script.cs` directly.

```yaml
hooks:
  postprovision:
    run: ./hooks/seed-database.cs
    # No .csproj nearby + .NET 10+ SDK → single-file execution
```

### .NET hook — explicit kind

```yaml
hooks:
  postprovision:
    run: ./hooks/setup
    kind: dotnet
```

### .NET hook with working directory override

```yaml
hooks:
  postprovision:
    run: ./tools/scripts/seed.cs
    dir: ./tools    # .csproj is in ./tools, not ./tools/scripts
```

## How It Works

Every hook follows the unified **Prepare → Execute → Cleanup** lifecycle:

1. **Prepare** — The executor validates prerequisites and performs any
   setup. This includes:
   - **Kind detection** from the explicit `kind` field, the
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
- **Phase 1** supports Python as a non-shell executor.
  **Phase 2** adds JavaScript, **Phase 3** adds TypeScript,
  and **Phase 4** adds .NET (C#).
- **Virtual environments** (Python) are created in the project directory alongside
  the dependency file, following the naming convention `{dirName}_env`.
- **TypeScript** hooks require Node.js 18+ and use `npx tsx` for execution.
  If `tsx` is not installed locally, `npx` will download it automatically.
- **Package manager** for JS/TS hooks currently uses npm for dependency
  installation. Support for pnpm and yarn may be added in a future release.
- **.NET single-file** execution (`.cs` without a `.csproj`) requires .NET SDK
  10.0.0 or later. On older SDKs, create a `.csproj` project file alongside
  the script.
