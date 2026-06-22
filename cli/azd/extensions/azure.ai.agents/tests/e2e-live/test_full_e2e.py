#!/usr/bin/env python3
"""Full E2E test: init -> provision -> deploy -> invoke -> down.

See README.md for complete setup & run instructions.

Prerequisites:
  - Linux (including WSL) with tmux (>=3.4), Python 3.12+ — also runs on the
    Azure DevOps Linux agent via eng/pipelines/ext-azure-ai-agents-live.yml
  - azd (>=1.25.5) with azure.ai.agents extension, logged in via `azd auth login`
  - For local WSL runs, leave auth.useAzCliAuth unset (azd built-in auth, via
    `azd config unset auth.useAzCliAuth`); under CI (GitHub Actions / Azure
    DevOps / E2E_USE_AZ_CLI_AUTH=true) the script auto-enables az CLI auth.
  - GitHub token available via gh.exe or $GITHUB_TOKEN

Recommended env vars:
  E2E_CREATE_PROJECT=true   — always create new Foundry project (avoid stale resources)
  E2E_LOCATION=eastus2      — region with sufficient model quota
  E2E_HOME=$HOME            — home directory for azd config
"""
import subprocess
import time
import sys
import os

import re
import shlex

TMUX = os.environ.get("E2E_TMUX", "tmux")
SOCK = os.environ.get("E2E_SOCK", "e2e")
SESS = os.environ.get("E2E_SESS", "e2e")
TESTDIR = os.environ.get("E2E_TESTDIR", "/tmp/e2e-tests/full-e2e")
HOME_DIR = os.environ.get("E2E_HOME", os.environ.get("HOME", "/home/runner"))
SUBSCRIPTION = os.environ.get("E2E_SUBSCRIPTION", "")
PROJECT = os.environ.get("E2E_PROJECT", "")
TENANT = os.environ.get("E2E_TENANT", "")
AGENT_NAME = os.environ.get("E2E_AGENT_NAME", "")  # Optional: unique name for parallel isolation
CREATE_PROJECT = os.environ.get("E2E_CREATE_PROJECT", "").lower() in ("1", "true", "yes")
LOCATION = os.environ.get("E2E_LOCATION", "eastus2")  # Region for new projects
# Inherit full parent PATH so tmux sessions get az-wrapper, azd, etc.
PARENT_PATH = os.environ.get("PATH", f"{HOME_DIR}/bin:/usr/local/bin:/usr/bin:/bin")
_tenant_env = f"; export AZURE_TENANT_ID={shlex.quote(TENANT)}" if TENANT else ""
# The GitHub token is intentionally NOT baked into ENV_SETUP. It is exported
# exactly once in setup() (immediately before the tmux scrollback is cleared),
# so the secret never lingers in pane history or gets duplicated across panes.
ENV_SETUP = f"export HOME={shlex.quote(HOME_DIR)}; export PATH={shlex.quote(PARENT_PATH)}{_tenant_env}"

# Track results
results = {}
DEPLOY_MODE = os.environ.get("E2E_DEPLOY_MODE", "code")  # "code" or "container"
_SENTINEL_BASE = "__DONE_{}_".format(os.getpid())
_sentinel_counter = 0


def get_gh_token():
    """Get GitHub token from env or gh CLI."""
    token = os.environ.get("GITHUB_TOKEN", os.environ.get("GH_TOKEN", ""))
    if token:
        return token
    # Try native gh CLI
    try:
        r = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True, timeout=10)
        if r.returncode == 0 and r.stdout.strip():
            return r.stdout.strip()
    except Exception:
        pass
    # Try Windows gh.exe (WSL local-dev only)
    if os.path.exists("/mnt/c"):
        try:
            r = subprocess.run(
                ["/mnt/c/Program Files/GitHub CLI/gh.exe", "auth", "token"],
                capture_output=True, text=True, timeout=10
            )
            if r.returncode == 0 and r.stdout.strip():
                return r.stdout.strip()
        except Exception:
            pass
    return ""


def tmux(*args):
    cmd = [TMUX, "-L", SOCK] + list(args)
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    if r.returncode != 0 and r.stderr:
        print(f"  [tmux error] {' '.join(args[:3])}: {r.stderr.strip()}")
    return r.stdout


def send(text):
    tmux("send-keys", "-t", SESS, "-l", text)


def key(k):
    tmux("send-keys", "-t", SESS, k)


def capture():
    return tmux("capture-pane", "-t", SESS, "-p")


def wait_for(pattern, timeout=60):
    deadline = time.time() + timeout
    while time.time() < deadline:
        cap = capture()
        if pattern.lower() in cap.lower():
            return cap
        time.sleep(1)
    return None


def wait_for_or_fail(pattern, timeout=60, phase=""):
    cap = wait_for(pattern, timeout)
    if cap is None:
        print(f"TIMEOUT waiting for: {pattern}")
        print("Last capture:")
        print(capture())
        if phase:
            results[phase] = "FAIL (timeout)"
        return None
    return cap


def select_by_text(target, delay=1.5):
    send(target)
    time.sleep(delay)
    key("Enter")


def show(label="", lines_count=15):
    cap = capture()
    lines = [l for l in cap.split("\n") if l.strip()]
    if label:
        print(f"\n--- {label} ---")
    for l in lines[-lines_count:]:
        print(f"  {l}")


def run_cmd(cmd, timeout=600):
    """Send command with unique sentinel and wait for completion. Returns (capture_text, exit_code).
    
    Each call uses a unique sentinel (base + counter) so that leftover output from
    previous commands cannot cause a false match.
    """
    global _sentinel_counter
    _sentinel_counter += 1
    sentinel = f"{_SENTINEL_BASE}{_sentinel_counter}_"
    sentinel_re = re.compile(re.escape(sentinel) + r"(\d+)")

    send(f"{cmd} ; echo {sentinel}$?")
    key("Enter")
    deadline = time.time() + timeout
    while time.time() < deadline:
        cap = capture()
        m = sentinel_re.search(cap)
        if m:
            rc = int(m.group(1))
            return cap, rc
        time.sleep(3)
    return None, -1


# Legacy: kept for reference, prefer run_cmd()
def _wait_for_shell_prompt_legacy(timeout=600):
    """Wait for bash prompt (command finished)."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        cap = capture()
        lines = [l for l in cap.split("\n") if l.strip()]
        if lines:
            last = lines[-1].strip()
            if last.endswith("$") or last.startswith("bash"):
                return cap
        time.sleep(3)
    return None


def validate_init_output(testdir):
    """Validate init produced correct artifacts on disk."""
    import glob as _glob
    for d in os.listdir(testdir):
        subdir = os.path.join(testdir, d)
        if os.path.isdir(subdir):
            azure_yaml = os.path.join(subdir, "azure.yaml")
            if os.path.exists(azure_yaml):
                with open(azure_yaml) as f:
                    content = f.read()
                if "host:" in content and "azure.ai.agent" in content:
                    # agent.yaml may be nested under src/<service>/
                    agent_yamls = _glob.glob(os.path.join(subdir, "**", "agent.yaml"), recursive=True)
                    if agent_yamls or os.path.exists(os.path.join(subdir, "agent.yaml")):
                        return True
    return False


def find_service_name(testdir):
    """Read the first service name from azure.yaml under the generated project."""
    for d in os.listdir(testdir):
        subdir = os.path.join(testdir, d)
        azure_yaml_path = os.path.join(subdir, "azure.yaml")
        if os.path.isdir(subdir) and os.path.exists(azure_yaml_path):
            with open(azure_yaml_path) as f:
                content = f.read()
            in_services = False
            for line in content.split("\n"):
                if line.strip() == "services:":
                    in_services = True
                    continue
                if in_services and line.startswith("  ") and line.strip().endswith(":"):
                    return line.strip().rstrip(":")
                if in_services and not line.startswith(" ") and line.strip():
                    break
    return None


def _assert_safe_testdir(path):
    """Guardrail before `rm -rf`: refuse a path that is not a clearly disposable
    test dir, so a bad E2E_TESTDIR (e.g. '/', '/tmp', '$HOME') can never trigger
    a destructive delete. Returns the normalized absolute path."""
    abspath = os.path.abspath(path)
    home = os.path.abspath(os.path.expanduser("~"))
    protected = {"/", "/tmp", "/var", "/usr", "/etc", "/bin", "/lib",
                 "/root", "/home", home}
    if abspath in protected or abspath.count("/") < 2:
        raise RuntimeError(
            f"Refusing to `rm -rf` unsafe E2E_TESTDIR={path!r} (resolved {abspath!r})")
    return abspath


# ===========================================================
# SETUP
# ===========================================================
def setup():
    global TESTDIR
    print("=" * 60)
    print("SETUP")
    print("=" * 60)

    # Bound kill-server so a wedged tmux cannot hang the whole CI job. It stays
    # best-effort (no check): a "no server running" error here is expected.
    subprocess.run([TMUX, "-L", SOCK, "kill-server"], capture_output=True, timeout=30)
    time.sleep(0.5)

    # Clean test dir (guard against a destructive E2E_TESTDIR like '/' or '/tmp').
    # Normalize to a vetted ABSOLUTE path and assign it back to the module global
    # so every later step (cd, payload --input-file, dir scans, teardown) acts on
    # the exact directory we wipe/create here — even if E2E_TESTDIR was relative.
    # check=True surfaces a failed delete instead of running against a dirty dir;
    # timeout keeps a stuck delete from stalling the job with no diagnostic.
    TESTDIR = _assert_safe_testdir(TESTDIR)
    subprocess.run(["rm", "-rf", "--", TESTDIR], check=True, timeout=120)
    os.makedirs(TESTDIR, exist_ok=True)

    # Create tmux session
    tmux("new-session", "-d", "-s", SESS, "-x", "200", "-y", "50", "bash --norc --noprofile")
    time.sleep(2)

    cap = capture()
    print(f"Session alive: {len(cap)} chars")

    # Set environment
    gh_token = get_gh_token()
    env_cmd = ENV_SETUP
    if gh_token:
        env_cmd += f"; export GH_TOKEN={shlex.quote(gh_token)}; export GITHUB_TOKEN={shlex.quote(gh_token)}"
        print(f"GitHub token: {len(gh_token)} chars")
    send(env_cmd)
    key("Enter")
    time.sleep(1)
    # Clear scrollback to avoid token leaking into capture output. `clear` only
    # wipes the visible screen, so also drop tmux's scrollback buffer — the GH
    # token was just typed into this pane and must never resurface in capture().
    send("clear")
    key("Enter")
    time.sleep(0.5)
    tmux("clear-history", "-t", SESS)

    send("echo ENV_OK")
    key("Enter")
    time.sleep(2)
    cap = capture()
    if "ENV_OK" not in cap:
        print("ERROR: Environment setup failed")
        sys.exit(1)
    print("Environment OK")

    # Auth config: CI uses az CLI (OIDC token), local WSL uses azd built-in auth.
    # In CI, the pipeline logs az CLI in via OIDC → azd needs useAzCliAuth=true.
    # In WSL, az CLI is slow (cross-process) → must use azd built-in auth.
    # Detection: GitHub Actions (GITHUB_ACTIONS), Azure DevOps (TF_BUILD), or an
    # explicit E2E_USE_AZ_CLI_AUTH override for other CI / manual runs.
    _use_az_cli_auth = (
        os.environ.get("E2E_USE_AZ_CLI_AUTH", "").lower() in ("1", "true", "yes")
        or bool(os.environ.get("GITHUB_ACTIONS"))
        or bool(os.environ.get("TF_BUILD"))  # Azure DevOps pipeline
    )
    if _use_az_cli_auth:
        send("azd config set auth.useAzCliAuth true")
    else:
        send("azd config unset auth.useAzCliAuth 2>/dev/null")
    key("Enter")
    time.sleep(1)

    send(f"cd {shlex.quote(TESTDIR)}")
    key("Enter")
    time.sleep(1)


# ===========================================================
# PHASE 1: INIT
# ===========================================================
def phase_init():
    print("\n" + "=" * 60)
    print("PHASE 1: azd ai agent init")
    print("=" * 60)

    init_cmd = "azd ai agent init"
    if AGENT_NAME:
        init_cmd += f" --agent-name {shlex.quote(AGENT_NAME)}"
    send(init_cmd)
    key("Enter")
    time.sleep(8)

    # Step 1: Language
    if not wait_for_or_fail("Select a language", 30, "init"):
        return False
    print("[1] Language: Python")
    select_by_text("Python")
    time.sleep(3)

    # Step 2: Template
    if not wait_for_or_fail("Select a starter template", 30, "init"):
        return False
    print("[2] Template: Basic agent (Invocations)")
    select_by_text("Basic agent (Invocations")
    time.sleep(8)

    # Step 2.5: Git protocol (may appear between template download and name prompt)
    time.sleep(3)
    cap = capture()
    if "protocol" in cap.lower() or "git operations" in cap.lower():
        print("[2.5] Git protocol: HTTPS (default)")
        key("Enter")
        time.sleep(3)

    # Step 3: Name (may be skipped if --agent-name was used)
    if AGENT_NAME:
        print(f"[3] Name: {AGENT_NAME} (via --agent-name, prompt may be skipped)")
        # Wait briefly for name prompt — if it doesn't appear, flag worked
        cap = wait_for("Enter a name", 15)
        if cap:
            key("Enter")
            time.sleep(5)
    else:
        if not wait_for_or_fail("Enter a name", 30, "init"):
            return False
        print("[3] Name: default")
        key("Enter")
        time.sleep(8)

    # Step 4: Foundry project type
    if not wait_for_or_fail("Select a Foundry project", 30, "init"):
        return False

    if CREATE_PROJECT:
        # Create a new Foundry project — azd manages all resources
        print("[4] Create a new Foundry project")
        select_by_text("Create")
        time.sleep(5)
        # Remaining prompts (subscription, location, names) handled by dynamic loop
    else:
        # Use existing Foundry project
        print("[4] Use existing Foundry project")
        key("Enter")

        # Step 5: Wait for subscription or project picker
        deadline = time.time() + 30
        while time.time() < deadline:
            time.sleep(3)
            cap = capture()
            lines = [l for l in cap.split("\n") if l.strip()]
            active_prompt = ""
            for l in reversed(lines):
                if l.strip().startswith("?"):
                    active_prompt = l.strip().lower()
                    break
            if "subscription" in active_prompt:
                print("[5] Subscription: accept default")
                key("Enter")
                time.sleep(10)
                if not wait_for_or_fail("Select a Foundry project", 30, "init"):
                    return False
                break
            elif "select a foundry project" in active_prompt and "use an existing" not in active_prompt:
                print("[5] Subscription: skipped (already on project picker)")
                break
            if lines and (lines[-1].strip().endswith("$") or lines[-1].strip().startswith("bash")):
                if any("error" in l.lower() for l in lines[-5:]):
                    print("[5] ERROR: CLI exited")
                    show("Error")
                    results["init"] = "FAIL (error)"
                    return False
        else:
            print("[5] Timeout waiting for subscription/project picker")
            show("Timeout")
            results["init"] = "FAIL (timeout step 5)"
            return False

        # Step 6: Project — verify we're on the project picker before typing
        cap = capture()
        cap_lines = [l for l in cap.split("\n") if l.strip()]
        last_prompt = ""
        for l in reversed(cap_lines):
            if l.strip().startswith("?"):
                last_prompt = l.strip().lower()
                break

        if "foundry project" in last_prompt or "project" in last_prompt:
            print(f"[6] Project: {PROJECT}")
            if PROJECT:
                select_by_text(PROJECT, delay=3)
            else:
                key("Enter")
            time.sleep(10)

            # Verify we're past the project picker (not stuck)
            time.sleep(3)
            cap = capture()
            prompt_line = ""
            for l in reversed(cap.split("\n")):
                if l.strip().startswith("?"):
                    prompt_line = l.strip().lower()
                    break
            if "select a foundry project" in prompt_line:
                print("[6b] Project filter may have failed, accepting highlighted")
                key("Enter")
                time.sleep(5)
        else:
            print(f"[6] Not on project picker, moving to dynamic")

    # Step 7+: Dynamic prompts
    _last_prompt = ""
    _same_prompt_count = 0
    for step_num in range(7, 45):
        time.sleep(3)
        cap = capture()
        cap_lower = cap.lower()

        if "added to your azd project" in cap_lower or "agent definition added" in cap_lower:
            print(f"[{step_num}] === INIT COMPLETE ===")
            if not validate_init_output(TESTDIR):
                print("  WARNING: marker found but disk validation failed, checking...")
                time.sleep(5)
                if not validate_init_output(TESTDIR):
                    print("  FAIL: artifacts not on disk despite completion marker")
                    results["init"] = "FAIL (no artifacts)"
                    return False
            results["init"] = "PASS"
            return True

        # Check for error exit
        lines = [l for l in cap.split("\n") if l.strip()]
        if lines:
            last = lines[-1].strip()
            if (last.endswith("$") or last.startswith("bash")):
                if "error" in cap_lower:
                    print(f"[{step_num}] Init exited with error")
                    show("Error")
                    results["init"] = "FAIL (error)"
                    return False

        # Find ? prompt
        prompt = ""
        for l in reversed(lines):
            if l.strip().startswith("?"):
                prompt = l.strip().lower()
                break

        if not prompt:
            time.sleep(5)
            cap = capture()
            lines = [l for l in cap.split("\n") if l.strip()]
            for l in reversed(lines):
                if l.strip().startswith("?"):
                    prompt = l.strip().lower()
                    break

        if not prompt:
            if lines and (lines[-1].strip().startswith("bash") or lines[-1].strip().endswith("$")):
                # Check if init completed without marker
                if validate_init_output(TESTDIR):
                    print(f"[{step_num}] Init complete (disk validation)")
                    results["init"] = "PASS"
                    return True
                print(f"[{step_num}] Shell prompt, no completion marker")
                show("Final")
                results["init"] = "FAIL (no completion)"
                return False
            print(f"[{step_num}] Waiting...")
            continue

        print(f"[{step_num}] {prompt[:80]}")

        # Detect prompt loops — same prompt question repeating 3+ times
        # Compare by question part before ':' to handle varying filter text
        colon_idx = prompt.find(":")
        prompt_key = prompt[:colon_idx].strip() if colon_idx > 0 else prompt.strip()
        if prompt_key == _last_prompt:
            _same_prompt_count += 1
        else:
            _same_prompt_count = 1
            _last_prompt = prompt_key

        if _same_prompt_count >= 3:
            print(f"  !! Loop detected ({_same_prompt_count}x same prompt)")
            if "model" in prompt or "is specified" in prompt:
                # Model prompt looping — probably no quota. Try Down to pick alt option.
                print("  -> navigating to alternative option")
                key("Down")
                time.sleep(0.3)
                key("Enter")
                time.sleep(3)
                continue
            elif _same_prompt_count >= 5:
                print("  FAIL: stuck in prompt loop")
                results["init"] = "FAIL (prompt loop)"
                return False

        # Handle prompts
        if "[y/n]" in prompt or "(y/n)" in prompt:
            # Confirm prompts — answer yes unless it's asking to reuse a conflicting name
            if "continue with this existing agent name" in prompt:
                print("  -> no (use fresh name)")
                send("n")
                key("Enter")
            else:
                print("  -> yes")
                send("y")
                key("Enter")
        elif "protocol" in prompt or "git operations" in prompt:
            # "What is your preferred protocol for Git operations?" → HTTPS (default)
            print("  -> HTTPS (default)")
            key("Enter")
        elif "enter a different name" in prompt:
            print("  -> default name")
            key("Enter")
        elif "acr" in prompt or "container registry" in prompt:
            print("  -> blank (create new)")
            key("Enter")
        elif "enter model deployment name" in prompt or ("enter" in prompt and "deployment" in prompt and "name" in prompt):
            print("  -> default name")
            key("Enter")
        elif "existing deployment" in prompt or "is specified in the agent manifest" in prompt or ("found" in prompt and "deployment" in prompt):
            print("  -> use existing/specified")
            key("Enter")
        elif "capacity" in prompt:
            # Capacity field is usually pre-filled; accept default
            print("  -> accept capacity (default)")
            key("Enter")
        elif "sku" in prompt:
            print("  -> default SKU")
            key("Enter")
        elif "version" in prompt:
            print("  -> default version")
            key("Enter")
        elif "select" in prompt and "model" in prompt:
            print("  -> select gpt-4o-mini")
            select_by_text("gpt-4o-mini")
        elif "subscription" in prompt:
            if SUBSCRIPTION:
                print(f"  -> subscription: filter by {SUBSCRIPTION[:8]}")
                select_by_text(SUBSCRIPTION[:8], delay=2)
            else:
                print("  -> subscription: accept default")
                key("Enter")
        elif "location" in prompt or "region" in prompt:
            print(f"  -> location: {LOCATION}")
            select_by_text(LOCATION, delay=2)
        elif "foundry project" in prompt or ("select" in prompt and "project" in prompt):
            if PROJECT:
                print(f"  -> project: {PROJECT}")
                select_by_text(PROJECT, delay=3)
            else:
                print("  -> default project")
                key("Enter")
        elif "account name" in prompt or "resource name" in prompt or "hub name" in prompt:
            print("  -> accept default name")
            key("Enter")
        elif "model" in prompt and "capacity" not in prompt:
            print("  -> default model")
            key("Enter")
        elif "deploy" in prompt and ("mode" in prompt or "how" in prompt) and "capacity" not in prompt:
            if DEPLOY_MODE == "container":
                print("  -> Container")
                select_by_text("Container")
            else:
                print("  -> Source Code")
                select_by_text("Source")
        elif "what would you like to do" in prompt:
            # Accept "Exit setup" (default) to finish init.
            # Do NOT navigate up/down — that causes infinite loops by selecting
            # "Add another model" or similar options.
            print("  -> Exit setup (default)")
            key("Enter")
        else:
            print("  -> Enter (default)")
            key("Enter")
        time.sleep(3)

    results["init"] = "FAIL (too many steps)"
    return False


# ===========================================================
# PHASE 2: PROVISION
# ===========================================================
def phase_provision():
    print("\n" + "=" * 60)
    print("PHASE 2: azd provision")
    print("=" * 60)

    # Find the project subdirectory created by init
    project_dir = None
    for d in os.listdir(TESTDIR):
        subdir = os.path.join(TESTDIR, d)
        if os.path.isdir(subdir) and os.path.exists(os.path.join(subdir, "azure.yaml")):
            project_dir = subdir
            break

    if not project_dir:
        print("ERROR: No project directory with azure.yaml found")
        results["provision"] = "FAIL (no project dir)"
        return False

    print(f"Project dir: {project_dir}")
    send(f"cd {shlex.quote(project_dir)}")
    key("Enter")
    time.sleep(1)

    # Provision can take several minutes
    print("Waiting for provision to complete (up to 10 min)...")
    cap, rc = run_cmd("azd provision --no-prompt", timeout=600)
    if cap is None:
        print("TIMEOUT: provision did not complete in 10 min")
        show("Current state", 20)
        results["provision"] = "FAIL (timeout)"
        return False

    show("Provision result", 20)
    if rc != 0:
        print(f"Provision FAILED (exit code {rc})")
        results["provision"] = f"FAIL (exit code {rc})"
        return False

    print("Provision appears complete")
    results["provision"] = "PASS"
    return True


# ===========================================================
# PHASE 3: DEPLOY
# ===========================================================
def phase_deploy():
    print("\n" + "=" * 60)
    print("PHASE 3: azd deploy")
    print("=" * 60)

    # Deploy can take several minutes
    print("Waiting for deploy to complete (up to 10 min)...")
    cap, rc = run_cmd("azd deploy --no-prompt", timeout=600)
    if cap is None:
        print("TIMEOUT: deploy did not complete in 10 min")
        show("Current state", 20)
        results["deploy"] = "FAIL (timeout)"
        return False

    show("Deploy result", 20)
    if rc != 0:
        print(f"Deploy FAILED (exit code {rc})")
        results["deploy"] = f"FAIL (exit code {rc})"
        return False

    print("Deploy appears complete")
    results["deploy"] = "PASS"
    return True


# ===========================================================
# PHASE 4: INVOKE
# ===========================================================
def phase_invoke():
    print("\n" + "=" * 60)
    print("PHASE 4: azd ai agent invoke")
    print("=" * 60)

    # Wait for agent to fully start after deploy
    wait_secs = 60 if DEPLOY_MODE == "container" else 30
    print(f"Waiting {wait_secs}s for agent startup ({DEPLOY_MODE} mode)...")
    time.sleep(wait_secs)

    # The invocations protocol requires JSON payload via --input-file.
    # Positional message sends empty body to invocations agents (azd bug/limitation).
    service_name = find_service_name(TESTDIR)
    if not service_name:
        print("ERROR: Could not determine service name from azure.yaml")
        results["invoke"] = "FAIL (no service name)"
        return False
    print(f"  Service name: {service_name}")

    # Write payload to temp file for --input-file
    payload_file = os.path.join(TESTDIR, ".invoke-payload.json")
    with open(payload_file, "w") as f:
        f.write('{"message": "Hello, what is 2+2?"}')

    max_retries = 3
    for attempt in range(1, max_retries + 1):
        print(f"\nInvoke attempt {attempt}/{max_retries}...")
        cap, rc = run_cmd(
            f"azd ai agent invoke {shlex.quote(service_name)} --new-session -f {shlex.quote(payload_file)}",
            timeout=180,
        )
        if cap is None:
            print("TIMEOUT: invoke did not complete in 3 min")
            show("Current state", 20)
            if attempt == max_retries:
                results["invoke"] = "FAIL (timeout)"
                return False
            continue

        show("Invoke result", 20)

        # Check for errors
        # Look for ERROR line in last few lines of output
        lines = [l for l in cap.split("\n") if l.strip()]
        has_error = False
        error_msg = ""
        if rc != 0:
            for l in lines:
                if "ERROR:" in l or ("error" in l.lower() and "500" in l):
                    has_error = True
                    error_msg = l.strip()
                    break
            if not error_msg:
                error_msg = f"exit code {rc}"

        if rc != 0 and has_error and ("500" in error_msg or "Internal Server Error" in error_msg):
            print(f"  Server error: {error_msg[:100]}")
            if attempt < max_retries:
                print(f"  Retrying in 30s (container may still be starting)...")
                time.sleep(30)
                continue
            else:
                # Get container logs for debugging
                print("\n  Fetching agent logs for debugging...")
                send(f"azd ai agent monitor {shlex.quote(service_name)} --tail 50")
                key("Enter")
                time.sleep(10)
                log_cap = _wait_for_shell_prompt_legacy(timeout=60)
                if log_cap:
                    show("Agent logs", 30)
                results["invoke"] = f"FAIL (HTTP 500: {error_msg[:80]})"
                return False
        elif rc != 0:
            print(f"  Error: {error_msg[:100]}")
            if attempt < max_retries:
                time.sleep(15)
                continue
            results["invoke"] = f"FAIL ({error_msg[:80]})"
            return False
        else:
            # Success — verify response content
            # Extract lines between the LAST invoke command and its sentinel.
            # The capture may contain output from previous phases, so we must
            # find the last occurrence of the invoke command to avoid matching
            # stale sentinels from earlier phases (deploy, provision, etc.).
            all_lines = cap.split("\n")
            # Find the last line that contains the invoke command
            invoke_start = -1
            for i in range(len(all_lines) - 1, -1, -1):
                if "invoke" in all_lines[i].lower() and service_name in all_lines[i]:
                    invoke_start = i
                    break

            resp_lines = []
            if invoke_start >= 0:
                for line in all_lines[invoke_start + 1:]:
                    if _SENTINEL_BASE in line:
                        break
                    if line.strip():
                        resp_lines.append(line.strip())

            response_text = "\n".join(resp_lines)
            if not response_text.strip():
                print("  WARNING: invoke returned empty response")
                if attempt < max_retries:
                    print("  Retrying...")
                    time.sleep(15)
                    continue
                results["invoke"] = "FAIL (empty response)"
                return False

            # Payload asks "what is 2+2?". Accept a standalone "4" token or the
            # spelled-out word "four" (a live model may answer either). The regex
            # requires "4" to stand alone so unrelated "4"s in captured output —
            # model names ("gpt-4o-mini"), versions ("4.1"), or status codes
            # ("404") — don't produce a false pass.
            has_expected = (
                re.search(r"(?<![\w.])4(?!\.\d)(?!\w)", response_text) is not None
                or re.search(r"\bfour\b", response_text, re.IGNORECASE) is not None
            )
            print(f"  Response ({len(response_text)} chars): {response_text[:120]}")
            if not has_expected:
                print("  FAIL: response does not contain expected '4'/'four'")
                results["invoke"] = "FAIL (response missing '4'/'four')"
                return False

            results["invoke"] = "PASS"
            return True

    results["invoke"] = "FAIL (all retries exhausted)"
    return False


# ===========================================================
# PHASE 5: TEARDOWN
# ===========================================================
def phase_teardown():
    print("\n" + "=" * 60)
    print("PHASE 5: azd down (teardown)")
    print("=" * 60)

    print("Waiting for teardown (up to 10 min)...")
    cap, rc = run_cmd("azd down --force --purge --no-prompt", timeout=600)
    if cap is None:
        print("TIMEOUT: teardown did not complete")
        show("Current state", 20)
        results["teardown"] = "FAIL (timeout)"
        return False

    show("Teardown result", 20)
    if rc != 0:
        print(f"Teardown FAILED (exit code {rc})")
        results["teardown"] = f"FAIL (exit code {rc})"
        return False
    print("Teardown complete")
    results["teardown"] = "PASS"
    return True


# ===========================================================
# MAIN
# ===========================================================
if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("--keep", action="store_true", help="Keep deployed agent (skip teardown)")
    parser.add_argument("--deploy-mode", choices=["code", "container"], default="code",
                        help="Deploy mode: 'code' (Source Code) or 'container' (Container)")
    args = parser.parse_args()
    DEPLOY_MODE = args.deploy_mode

    print(f"Deploy mode: {DEPLOY_MODE}")
    start_time = time.time()

    setup()

    # Run phases sequentially
    init_ok = phase_init()
    if init_ok:
        if phase_provision():
            if phase_deploy():
                phase_invoke()
            if not args.keep:
                phase_teardown()
            else:
                print("\n>>> --keep flag: skipping teardown, agent remains deployed <<<")
                results["teardown"] = "SKIPPED (--keep)"
        else:
            if not args.keep:
                phase_teardown()
    else:
        # Init failed — but may have already created Azure resources (RG, project).
        # Attempt cleanup if there's a .azure directory indicating provisioned state.
        project_dir = None
        if os.path.isdir(TESTDIR):
            for d in os.listdir(TESTDIR):
                azure_dir = os.path.join(TESTDIR, d, ".azure")
                if os.path.isdir(azure_dir):
                    project_dir = os.path.join(TESTDIR, d)
                    break
        if project_dir and not args.keep:
            print(f"\nInit failed but found .azure in {project_dir} — attempting cleanup...")
            send(f"cd {shlex.quote(project_dir)}")
            key("Enter")
            time.sleep(1)
            phase_teardown()

    # Cleanup tmux
    tmux("kill-session", "-t", SESS)

    elapsed = time.time() - start_time
    print("\n" + "=" * 60)
    print(f"RESULTS (elapsed: {elapsed:.0f}s)")
    print("=" * 60)
    all_pass = True
    for phase, result in results.items():
        status = "✓" if "PASS" in result or "SKIPPED" in result else "✗"
        print(f"  {status} {phase}: {result}")
        if "FAIL" in result:
            all_pass = False

    required = ["init", "provision", "deploy", "invoke"]
    passed_required = all(results.get(p, "").startswith("PASS") for p in required)

    if passed_required:
        print("\n✓ ALL REQUIRED PHASES PASSED")
        sys.exit(0)
    else:
        missing = [p for p in required if not results.get(p, "").startswith("PASS")]
        print(f"\n✗ FAILED PHASES: {', '.join(missing)}")
        sys.exit(1)
