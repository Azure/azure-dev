## Reference: Prerequisites & Setup

### 1. Install `azd`

Everything below requires `azd` on the PATH. Detect it first:

```bash
azd version
```

If `azd` is **not installed**, install it (then re-run `azd version`):

```bash
# macOS / Linux
curl -fsSL https://aka.ms/install-azd.sh | bash
# Windows (PowerShell)
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```

Or use a package manager (`brew install azure-dev`, `winget install microsoft.azd`). Full options:
<https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd>. Use `azd` >= 1.23.7
for the current SDK helpers. The agent should **not** silently install `azd` on the user's machine
without saying so — surface the command and, for interactive sessions, confirm first.

### 2. Install the developer extension (`azd x`) — auto-bootstrap

The `azd x` command suite is provided by the `microsoft.azd.extensions` extension. It ships in the
**official** registry, which is **pre-configured** in `azd` — so no extra source is required, and
the `extension` command group is available out of the box (no alpha flag needed).

**If `azd x` is not installed, the skill can install it.** Run this idempotent bootstrap — it
detects the developer extension and installs it only when missing:

```bash
# Detect: does `azd x` already work?
if azd x version >/dev/null 2>&1; then
  echo "azd x already installed"
else
  # Install from the pre-configured official registry (no source setup needed)
  azd extension install microsoft.azd.extensions
fi

# Verify
azd x version && azd x --help
```

Keep it current with:

```bash
azd extension upgrade microsoft.azd.extensions
```

> **Optional — newer/experimental builds.** The official registry is enough for normal use. Only if
> the user needs a pre-release build should you add the **dev** (unsigned, experimental) or
> **nightly** source, then install from there:
>
> ```bash
> azd extension source add -n dev -t url -l "https://aka.ms/azd/extensions/registry/dev"
> azd extension install microsoft.azd.extensions --source dev
> ```
>
> The dev/nightly registries are **unsigned**, carry no stability guarantees, and are not a support
> channel.

### 3. Language toolchain

Install the toolchain for your chosen language:

- **Go** — Go 1.22+ (the azure-dev repo targets Go 1.26+; match `cli/azd/go.mod` for first-party).
- **.NET** — .NET SDK 8+.
- **JavaScript** — Node.js LTS.
- **Python** — Python 3.10+ (note: Python builds are slow, ~4 min vs ~15s for Go).

### 4. GitHub authentication (only for `release` / `publish`)

`azd x release` and `azd x publish` create GitHub releases and read release assets, so they need a
token with `repo` scope:

```bash
gh auth login
# or
export GITHUB_TOKEN=your_personal_access_token
```

Release/publish fail without valid GitHub auth.

### 5. Working directory conventions

- **First-party** (inside azure-dev): run `azd x init` **from `cli/azd/extensions/`**. The scaffold
  is created as a subdirectory named after the extension id.
- **External**: run `azd x init` from wherever you want the extension project to live.
- Most `azd x` commands operate on the **current directory** and accept `-C, --cwd <dir>` to point
  at the extension project.

### One-shot bootstrap the agent should run first

This verifies `azd`, then installs the developer extension only if missing — safe to run every
time:

```bash
azd version >/dev/null 2>&1 || { echo "azd not installed — see step 1"; exit 1; }
azd x version >/dev/null 2>&1 || azd extension install microsoft.azd.extensions
azd x version && echo "OK: azd + developer extension present"
```

If `azd` itself is missing, install it first (step 1) — surface the command to the user rather than
installing silently.
