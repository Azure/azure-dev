## Reference: Build, Install & Test

All commands run from the extension project directory (or pass `-C, --cwd <dir>`).

### Build

```bash
# Build for the current OS/arch and install locally (default)
azd x build

# Build for all supported platforms (win/linux/darwin × amd64/arm64)
azd x build --all

# Build without reinstalling
azd x build --skip-install

# Custom output directory (default ./bin)
azd x build --output ./out
```

`azd x build` invokes the language build script (`build.sh` / `build.ps1`), produces binaries in
`./bin`, and — unless `--skip-install` — installs the freshly built extension so `azd <namespace>`
runs your new code immediately. Metadata-validation warnings are printed after the build and are
non-fatal.

You can also build directly with the language toolchain (e.g. `go build`) for a quick compile
check, but `azd x build` is what wires the install.

### Watch (fast dev loop)

```bash
azd x watch
```

Watches the project and rebuilds + reinstalls on change. Add a `.azdxignore` file (gitignore
syntax; `.gitignore` is also honored) to exclude paths like `dist/`, `build/`, `*.tmp`.

### Install / uninstall manually

```bash
azd extension list --installed          # show installed extensions
azd extension install <id>              # from a configured source
azd extension install <bundle.zip>      # from a self-contained bundle (see pack --bundle)
azd extension uninstall <id>
azd extension upgrade <id>
```

For rapid local iteration of a first-party extension you can also copy binaries into the install
dir (as the developer extension's own README shows):

```bash
azd x build --skip-install
cp -f bin/* ~/.azd/extensions/<id>/
```

### Test the extension end to end

```bash
# Root/help wiring
azd <namespace> --help

# A custom command
azd <namespace> <command> [--flags] -e <env>

# The MCP server (stdio) — list tools
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | azd <namespace> mcp

# Lifecycle handler — fires during the real workflow
azd provision   # triggers e.g. a postprovision handler
```

### Automated tests

- **Unit tests**: standard per-language tests alongside sources (`*_test.go` for Go). Prefer
  table-driven tests; use `testify/mock` for Go mocks.
- **Go testing conventions (first-party)**: never write to `os.Stdout` in tests (use `t.Log`,
  `io.Discard`/`bytes.Buffer` for UX writers); use `t.Context()` and `t.Chdir()`. Keep lines ≤ 125
  chars (`lll`). Run `go build` before tests that spawn the CLI.
- **Snapshot tests (first-party only)**: when a first-party extension is added to `registry.json`,
  CI runs snapshot tests that assert the extension's commands appear correctly in `azd` help and
  VS Code IntelliSense. Update snapshots from `cli/azd`:

  ```bash
  UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'
  ```

### Debugging

Set `AZD_EXT_DEBUG` before invoking the extension to have `azd x build`/`watch` wait for a debugger
to attach (the scaffold wires `azdext.WaitForDebugger`).

### Verify checklist before moving to release

- `azd x build` succeeds for the current platform (and `--all` before releasing).
- `azd <namespace> --help` lists the expected commands.
- Declared `capabilities` in `extension.yaml` match what the code implements.
- Unit tests pass; first-party snapshot tests updated if `registry.json` changed.

### Troubleshooting install & discovery

- **`azd x` not found** — install the developer extension: `azd extension install
  microsoft.azd.extensions` (it's in the pre-configured official registry). Then `azd x version`.
- **Extension not appearing after install** — start a new `azd` invocation (commands are bound at
  startup); confirm with `azd extension list --installed` and check the namespace via
  `azd <namespace> --help`.
- **Command/namespace collision** — a namespace maps to a command path (dots become nested
  groups). Pick a unique namespace so it doesn't shadow a core `azd` command.
- **Capability mismatch** — a command/handler doesn't run because the matching capability isn't
  declared in `extension.yaml` (or a `*-provider` capability lacks its `providers:` entry). Keep the
  manifest and code in sync, then rebuild.
- **Stale build** — rebuild + reinstall with `azd x build` (or `azd x watch`); `--skip-install`
  leaves the old installed binary in place.
