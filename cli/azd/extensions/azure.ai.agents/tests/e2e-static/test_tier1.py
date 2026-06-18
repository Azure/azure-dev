#!/usr/bin/env python3
"""Tier 1: Interactive init variants. Requires Azure auth, no deploy.
Runs multiple init scenarios in parallel tmux sessions.
Validates: scaffold completes, azure.yaml generated correctly.
"""
import subprocess
import time
import sys
import os
import shutil
import tempfile
from concurrent.futures import ThreadPoolExecutor, as_completed

HOME_DIR = os.environ.get("E2E_HOME", os.environ.get("HOME", "/home/runner"))
TMUX = os.environ.get("E2E_TMUX", shutil.which("tmux") or "/usr/bin/tmux")
SUBSCRIPTION = os.environ.get("E2E_SUBSCRIPTION", "")
PROJECT = os.environ.get("E2E_PROJECT", "")
TENANT = os.environ.get("E2E_TENANT", "")
ENV_SETUP = f"export HOME={HOME_DIR}; export PATH={HOME_DIR}/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

RESULTS = []


def get_gh_token():
    token = os.environ.get("GITHUB_TOKEN", os.environ.get("GH_TOKEN", ""))
    if token:
        return token
    try:
        r = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True, timeout=10)
        if r.returncode == 0 and r.stdout.strip():
            return r.stdout.strip()
    except Exception:
        pass
    # WSL local-dev fallback
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


def check(name, passed, detail=""):
    RESULTS.append((name, passed, detail))
    status = "✓" if passed else "✗"
    print(f"  {status} {name}" + (f" — {detail}" if detail and not passed else ""))
    return passed


class TmuxSession:
    """Manages a tmux session for one test scenario."""

    def __init__(self, name, sock="tier1"):
        self.name = name
        self.sock = sock

    def start(self):
        subprocess.run([TMUX, "-L", self.sock, "kill-session", "-t", self.name],
                       capture_output=True)
        subprocess.run([TMUX, "-L", self.sock, "new-session", "-d", "-s", self.name,
                        "-x", "200", "-y", "50", "bash --norc --noprofile"],
                       capture_output=True, check=True)
        time.sleep(1)

    def send(self, text):
        subprocess.run([TMUX, "-L", self.sock, "send-keys", "-t", self.name, "-l", text],
                       capture_output=True, timeout=5)

    def key(self, k):
        subprocess.run([TMUX, "-L", self.sock, "send-keys", "-t", self.name, k],
                       capture_output=True, timeout=5)

    def capture(self):
        r = subprocess.run([TMUX, "-L", self.sock, "capture-pane", "-t", self.name, "-p"],
                           capture_output=True, text=True, timeout=5)
        return r.stdout

    def select_by_text(self, target, delay=1.5):
        """Type filter text and press Enter to select matching item."""
        self.send(target)
        time.sleep(delay)
        self.key("Enter")

    def wait_for(self, pattern, timeout=60):
        deadline = time.time() + timeout
        while time.time() < deadline:
            cap = self.capture()
            if pattern.lower() in cap.lower():
                return cap
            time.sleep(1)
        return None

    def setup_env(self, workdir):
        gh_token = get_gh_token()
        env_cmd = ENV_SETUP
        if gh_token:
            env_cmd += f"; export GH_TOKEN={gh_token}; export GITHUB_TOKEN={gh_token}"
        # Chain all setup commands; clear at end to start with clean pane
        env_cmd += "; azd config set auth.useAzCliAuth true 2>/dev/null"
        self.send(env_cmd)
        self.key("Enter")
        time.sleep(1)
        self.send(f"mkdir -p {workdir} && cd {workdir}")
        self.key("Enter")
        time.sleep(0.5)
        self.send("clear")
        self.key("Enter")
        time.sleep(0.3)

    def kill(self):
        subprocess.run([TMUX, "-L", self.sock, "kill-session", "-t", self.name],
                       capture_output=True)


def _validate_init_disk(workdir):
    """Check if init produced azure.yaml with host: azure.ai.agent and agent.yaml somewhere."""
    import glob as _glob
    # Search in subdirectories of workdir (template creates a folder like basic-agent/)
    for d in os.listdir(workdir):
        subdir = os.path.join(workdir, d)
        if os.path.isdir(subdir):
            azure_yaml = os.path.join(subdir, "azure.yaml")
            if os.path.exists(azure_yaml):
                with open(azure_yaml) as f:
                    content = f.read()
                if "host:" in content and "azure.ai.agent" in content:
                    # agent.yaml may be nested under src/<service>/
                    agent_files = _glob.glob(os.path.join(subdir, "**/agent.yaml"), recursive=True)
                    if agent_files:
                        return True
    # Also check workdir itself
    azure_yaml = os.path.join(workdir, "azure.yaml")
    if os.path.exists(azure_yaml):
        with open(azure_yaml) as f:
            content = f.read()
        if "host:" in content and "azure.ai.agent" in content:
            agent_files = _glob.glob(os.path.join(workdir, "**/agent.yaml"), recursive=True)
            if agent_files:
                return True
    return False


def handle_dynamic_prompts(sess, max_steps=40, deploy_mode="code", project_path="existing", verbose=False, workdir=None):
    """Handle dynamic prompts after template selection until init completes.
    project_path: "existing" uses existing project, "create" creates new.
    workdir: if provided, uses disk validation for completion check."""
    for step_num in range(max_steps):
        time.sleep(3)
        cap = sess.capture()
        cap_lower = cap.lower()

        # Check completion
        if "added to your azd project" in cap_lower or "agent definition added" in cap_lower:
            if verbose:
                print(f"    [dyn-{step_num}] COMPLETE (marker found)")
            return True

        # Check for shell prompt (command exited)
        lines = [l for l in cap.split("\n") if l.strip()]
        if lines:
            last = lines[-1].strip()
            if last.endswith("$") or last.startswith("bash"):
                if "error" in cap_lower:
                    if verbose:
                        print(f"    [dyn-{step_num}] ERROR exit: {last}")
                    return False
                # Disk-based validation if workdir provided
                if workdir:
                    if _validate_init_disk(workdir):
                        if verbose:
                            print(f"    [dyn-{step_num}] COMPLETE (disk validation)")
                        return True
                if verbose:
                    print(f"    [dyn-{step_num}] Shell prompt, no completion: {last}")
                return False

        # Find ? prompt
        prompt = ""
        for l in reversed(lines):
            if l.strip().startswith("?"):
                prompt = l.strip().lower()
                break

        if not prompt:
            if verbose:
                print(f"    [dyn-{step_num}] no ? prompt, waiting...")
            time.sleep(3)
            continue

        if verbose:
            print(f"    [dyn-{step_num}] prompt: {prompt[:80]}")

        # Handle prompts
        if "[y/n]" in prompt or "(y/n)" in prompt:
            if "continue with this existing agent name" in prompt:
                sess.send("n")
                sess.key("Enter")
            else:
                sess.send("y")
                sess.key("Enter")
        elif "existing deployment" in prompt or ("found" in prompt and "deployment" in prompt):
            # "Found N existing deployment(s) for model X in the selected foundry..."
            sess.key("Enter")
        elif "is specified in the agent manifest" in prompt:
            sess.key("Enter")
        elif "protocol" in prompt or "git operations" in prompt:
            # "What is your preferred protocol for Git operations?" → HTTPS (default)
            sess.key("Enter")
        elif "enter a name" in prompt and "deployment" not in prompt:
            # "Enter a name for this agent" → accept default
            sess.key("Enter")
        elif "foundry project" in prompt and "host" in prompt:
            # Initial "Select a Foundry project to host..." prompt (use existing vs create)
            if project_path == "existing":
                sess.key("Enter")  # "Use an existing" is default
                time.sleep(3)
            else:
                sess.select_by_text("Create")
                time.sleep(3)
        elif "select subscription" in prompt:
            # Accept the default/first subscription. In CI the correct one is logged in.
            # If multiple are available and SUBSCRIPTION is set, try to filter.
            if SUBSCRIPTION:
                # Check if there's already filter text corrupting the picker
                cap_now = sess.capture()
                if "no options found" in cap_now.lower():
                    # Clear filter and accept default
                    sess.key("Escape")
                    time.sleep(1)
                sess.key("Enter")
            else:
                sess.key("Enter")
            time.sleep(5)
        elif "foundry project" in prompt or ("select" in prompt and "project" in prompt):
            # Actual project selection picker after choosing "Use existing"
            if PROJECT:
                sess.select_by_text(PROJECT, delay=3)  # Extra delay for filter
                time.sleep(3)
                # Check if filter matched
                cap_now = sess.capture()
                if "no options found" in cap_now.lower():
                    # PROJECT not in this subscription; clear and accept first
                    sess.key("Escape")
                    time.sleep(1)
                    sess.key("Enter")
            else:
                sess.key("Enter")  # accept first available
            time.sleep(5)
        elif "enter a different name" in prompt:
            sess.key("Enter")
        elif "capacity" in prompt:
            # Capacity text input — clear existing text and type value.
            # ProvisionedManaged SKU often requires minimum 25-50 units.
            # Use Backspace to clear any pre-filled text, then type value.
            for _ in range(10):
                sess.key("BSpace")
            time.sleep(0.3)
            sess.send("50")
            time.sleep(0.5)
            sess.key("Enter")
        elif "enter model deployment name" in prompt or ("enter" in prompt and "deployment" in prompt and "name" in prompt):
            sess.key("Enter")
        elif "acr" in prompt or "container registry" in prompt:
            sess.key("Enter")
        elif "deploy" in prompt and "existing" not in prompt and "capacity" not in prompt:
            # Deploy mode/type/strategy prompt — select code or container
            if deploy_mode == "container":
                sess.select_by_text("Container")
            else:
                sess.select_by_text("Source")
            time.sleep(3)
        elif "application insights" in prompt:
            sess.key("Enter")
        elif "sku" in prompt:
            sess.key("Enter")
        elif "version" in prompt:
            sess.key("Enter")
        elif "select" in prompt and "model" in prompt:
            sess.select_by_text("gpt")
            time.sleep(3)
        elif "model" in prompt:
            sess.key("Enter")
        elif "region" in prompt or "location" in prompt:
            sess.select_by_text("northcentralus")
            time.sleep(3)
        elif "resource group" in prompt:
            sess.key("Enter")  # accept default
        elif "account name" in prompt or "resource name" in prompt:
            sess.key("Enter")  # accept default
        elif "what would you like to do" in prompt:
            # Move off "exit setup" default and select alternative (Continue/Skip)
            sess.key("Down")
            time.sleep(0.5)
            sess.key("Enter")
            time.sleep(2)
        else:
            if verbose:
                print(f"    [dyn-{step_num}] unhandled, pressing Enter")
            sess.key("Enter")
        time.sleep(2)

    return False


# ============================================================
# TEST SCENARIOS
# ============================================================

def test_init_python_basic_code(workdir):
    """Init Python basic template with code deploy, existing project."""
    sess = TmuxSession("t1-py-code")
    try:
        sess.start()
        sess.setup_env(workdir)

        sess.send("azd ai agent init")
        sess.key("Enter")
        time.sleep(5)

        # Language
        if not sess.wait_for("Select a language", 30):
            return check("01-init-python-code", False, "timeout at language")
        sess.select_by_text("Python")
        time.sleep(3)

        # Template
        if not sess.wait_for("Select a starter template", 30):
            return check("01-init-python-code", False, "timeout at template")
        sess.select_by_text("Basic agent (Responses")
        time.sleep(3)

        # After template selection, CLI flow varies (git protocol, name, foundry, etc.)
        # Let dynamic handler manage all remaining prompts.
        ok = handle_dynamic_prompts(sess, max_steps=40, deploy_mode="code",
                                    project_path="existing", workdir=workdir)

        # Verify artifacts on disk
        has_artifacts = _validate_init_disk(workdir)

        return check("01-init-python-code", ok and has_artifacts,
                     "" if (ok and has_artifacts) else f"ok={ok}, artifacts={has_artifacts}")
    finally:
        sess.kill()


def test_init_python_basic_container(workdir):
    """Init Python basic template with container deploy, existing project."""
    sess = TmuxSession("t1-py-ctr")
    try:
        sess.start()
        sess.setup_env(workdir)

        sess.send("azd ai agent init")
        sess.key("Enter")
        time.sleep(5)

        if not sess.wait_for("Select a language", 30):
            return check("01-init-python-container", False, "timeout at language")
        sess.select_by_text("Python")
        time.sleep(3)

        if not sess.wait_for("Select a starter template", 30):
            return check("01-init-python-container", False, "timeout at template")
        sess.select_by_text("Basic agent (Responses")
        time.sleep(3)

        # After template selection, let dynamic handler manage everything
        ok = handle_dynamic_prompts(sess, max_steps=40, deploy_mode="container",
                                    project_path="existing", workdir=workdir)
        return check("01-init-python-container", ok, "" if ok else "init did not complete")
    finally:
        sess.kill()


def test_init_csharp_basic(workdir):
    """Init C# Hello World template with code deploy."""
    sess = TmuxSession("t1-cs")
    try:
        sess.start()
        sess.setup_env(workdir)

        sess.send("azd ai agent init")
        sess.key("Enter")
        time.sleep(5)

        if not sess.wait_for("Select a language", 30):
            return check("01-init-csharp", False, "timeout at language")
        sess.select_by_text("C#")
        time.sleep(3)

        if not sess.wait_for("Select a starter template", 30):
            return check("01-init-csharp", False, "timeout at template")
        # C# has no "Basic" template, use "Hello World"
        sess.select_by_text("Hello World")
        time.sleep(3)

        # After template selection, let dynamic handler manage everything
        ok = handle_dynamic_prompts(sess, max_steps=40, deploy_mode="code",
                                    project_path="existing", workdir=workdir)
        return check("01-init-csharp", ok, "" if ok else "init did not complete")
    finally:
        sess.kill()


def test_init_create_new_project(workdir):
    """Init with 'Create new' Foundry project path (validates prompts, Ctrl-C before real creation)."""
    sess = TmuxSession("t1-new-proj")
    try:
        sess.start()
        sess.setup_env(workdir)

        sess.send("azd ai agent init")
        sess.key("Enter")
        time.sleep(5)

        if not sess.wait_for("Select a language", 30):
            return check("01-init-create-project", False, "timeout at language")
        sess.select_by_text("Python")
        time.sleep(3)

        if not sess.wait_for("Select a starter template", 30):
            return check("01-init-create-project", False, "timeout at template")
        sess.select_by_text("Basic agent (Responses")
        time.sleep(3)

        # Wait for Foundry project prompt (handles intermediate prompts like git protocol, name)
        # Use dynamic handler in "create" mode but with a special twist:
        # We just need to get to the "Create" path and verify it shows follow-up prompts.
        # First, handle any intermediate prompts until we see "foundry project"
        deadline = time.time() + 90
        found_foundry = False
        while time.time() < deadline:
            time.sleep(3)
            cap = sess.capture()
            cap_lower = cap.lower()
            if "foundry project" in cap_lower and "host" in cap_lower:
                found_foundry = True
                break
            # Handle intermediate prompts
            lines = [l for l in cap.split("\n") if l.strip()]
            prompt = ""
            for l in reversed(lines):
                if l.strip().startswith("?"):
                    prompt = l.strip().lower()
                    break
            if prompt:
                if "protocol" in prompt or "git operations" in prompt:
                    sess.key("Enter")
                elif "enter a name" in prompt:
                    sess.key("Enter")
                elif "what would you like to do" in prompt:
                    sess.select_by_text("Continue")
                else:
                    sess.key("Enter")
            time.sleep(2)

        if not found_foundry:
            return check("01-init-create-project", False, "timeout at foundry")

        # Select "Create a new" project
        sess.select_by_text("Create")
        time.sleep(5)

        # Should get subscription prompt or region prompt
        cap = sess.wait_for("select", 30)
        if cap is None:
            return check("01-init-create-project", False, "no follow-up prompt after Create")

        # We've confirmed the Create path works (shows further prompts).
        # Ctrl-C to exit without actually creating resources.
        sess.key("C-c")
        time.sleep(2)

        return check("01-init-create-project", True, "create path prompts verified")
    finally:
        sess.kill()


def test_init_from_manifest_url(workdir):
    """Init from manifest URL (non-interactive path)."""
    sess = TmuxSession("t1-manifest")
    try:
        sess.start()
        sess.setup_env(workdir)

        manifest_url = "https://raw.githubusercontent.com/microsoft-foundry/foundry-samples/main/samples/python/hosted-agents/agent-framework/responses/04-foundry-toolbox/agent.manifest.yaml"
        sess.send(f'azd ai agent init -m "{manifest_url}"')
        sess.key("Enter")
        time.sleep(5)

        # With -m flag, may skip language/template and go directly to other prompts.
        # Use dynamic handler for everything after the command is sent.
        ok = handle_dynamic_prompts(sess, max_steps=40, deploy_mode="code",
                                    project_path="existing", workdir=workdir)

        has_artifacts = _validate_init_disk(workdir)
        return check("01-init-manifest-url", ok or has_artifacts,
                     "" if (ok or has_artifacts) else "init did not complete")
    finally:
        sess.kill()


def test_init_with_agent_name_flag(workdir):
    """Init with --agent-name flag to skip name prompt and verify name in artifacts."""
    sess = TmuxSession("t1-agentname")
    try:
        sess.start()
        sess.setup_env(workdir)

        sess.send("azd ai agent init --agent-name my-custom-agent")
        sess.key("Enter")
        time.sleep(5)

        if not sess.wait_for("Select a language", 30):
            return check("01-init-agent-name-flag", False, "timeout at language")
        sess.select_by_text("Python")
        time.sleep(3)

        if not sess.wait_for("Select a starter template", 30):
            return check("01-init-agent-name-flag", False, "timeout at template")
        sess.select_by_text("Basic agent (Responses")
        time.sleep(3)

        # --agent-name should skip the "Enter a name" prompt.
        # Use dynamic handler for all remaining prompts.
        ok = handle_dynamic_prompts(sess, max_steps=40, deploy_mode="code",
                                    project_path="existing", workdir=workdir)

        # Validate: agent name must appear in produced artifacts
        time.sleep(3)
        found_name = False
        for d in os.listdir(workdir):
            subdir = os.path.join(workdir, d)
            if os.path.isdir(subdir):
                agent_yaml = os.path.join(subdir, "agent.yaml")
                azure_yaml = os.path.join(subdir, "azure.yaml")
                for fpath in [agent_yaml, azure_yaml]:
                    if os.path.exists(fpath):
                        with open(fpath) as f:
                            if "my-custom-agent" in f.read():
                                found_name = True
                                break
            if found_name:
                break

        return check("01-init-agent-name-flag", found_name,
                     "my-custom-agent found in artifacts" if found_name else "name not in artifacts")
    finally:
        sess.kill()


def test_init_bad_deploy_mode(workdir):
    """Init with invalid --deploy-mode flag should error."""
    r = subprocess.run(
        ["bash", "-c", f"{ENV_SETUP}; cd {workdir} && azd ai agent init --deploy-mode banana 2>&1"],
        capture_output=True, text=True, timeout=30
    )
    has_error = r.returncode != 0 or "error" in r.stdout.lower() or "invalid" in r.stdout.lower()
    return check("01-init-bad-deploy-mode", has_error,
                 f"exit={r.returncode}, out='{r.stdout.strip()[:80]}'")


def test_init_no_prompt_with_manifest(workdir):
    """Init with --no-prompt and manifest should not hang. May succeed or exit with error."""
    manifest_url = "https://raw.githubusercontent.com/microsoft-foundry/foundry-samples/main/samples/python/hosted-agents/agent-framework/responses/04-foundry-toolbox/agent.manifest.yaml"
    # Get GH token so git operations don't block on credential prompts
    gh_token = get_gh_token()
    env_extra = ""
    if gh_token:
        env_extra = f"export GH_TOKEN={gh_token}; export GITHUB_TOKEN={gh_token}; "
    try:
        r = subprocess.run(
            ["bash", "-c", f"{ENV_SETUP}; {env_extra}cd {workdir} && azd ai agent init -m '{manifest_url}' --no-prompt 2>&1"],
            capture_output=True, text=True, timeout=120
        )
        # --no-prompt should not hang. If it completes (any exit code), the test passes
        # because it proves the CLI handles non-interactive mode gracefully.
        # Full artifacts may not be created (no subscription/project config without prompts).
        if r.returncode == 0:
            has_artifacts = _validate_init_disk(workdir)
            if has_artifacts:
                return check("01-init-no-prompt-manifest", True, "exit=0, full artifacts")
            # exit=0 without full artifacts is acceptable — CLI handled gracefully
            return check("01-init-no-prompt-manifest", True,
                         f"exit=0 (graceful, partial setup)")
        else:
            # Non-zero exit is acceptable (e.g., missing project/subscription config)
            # as long as it didn't hang
            output = r.stdout.strip()[:120]
            return check("01-init-no-prompt-manifest", True,
                         f"exit={r.returncode} (graceful error): {output}")
    except subprocess.TimeoutExpired:
        # Timeout means it hung — FAIL (--no-prompt should never hang)
        return check("01-init-no-prompt-manifest", False,
                     "timeout: --no-prompt should not hang")


# ============================================================
# MAIN
# ============================================================
ALL_TESTS = [
    ("01-init-python-code", test_init_python_basic_code),
    ("01-init-python-container", test_init_python_basic_container),
    ("01-init-csharp", test_init_csharp_basic),
    ("01-init-create-project", test_init_create_new_project),
    ("01-init-manifest-url", test_init_from_manifest_url),
    ("01-init-agent-name-flag", test_init_with_agent_name_flag),
    ("01-init-bad-deploy-mode", test_init_bad_deploy_mode),
    ("01-init-no-prompt-manifest", test_init_no_prompt_with_manifest),
]

if __name__ == "__main__":
    print("=" * 60)
    print(f"TIER 1: Init Variants ({len(ALL_TESTS)} tests)")
    print("=" * 60)
    print(f"  Project: {PROJECT}")
    print(f"  Subscription: {SUBSCRIPTION[:8]}...")
    print()

    # Kill any leftover tmux sessions
    subprocess.run([TMUX, "-L", "tier1", "kill-server"], capture_output=True)
    time.sleep(0.5)

    start = time.time()

    # Create temp work directories
    base_dir = "/tmp/tier1-tests"
    subprocess.run(["rm", "-rf", base_dir])
    os.makedirs(base_dir, exist_ok=True)

    # Separate interactive (tmux) vs non-interactive tests
    interactive_tests = [t for t in ALL_TESTS if t[0] not in
                         ("01-init-bad-deploy-mode", "01-init-no-prompt-manifest")]
    non_interactive_tests = [t for t in ALL_TESTS if t[0] in
                             ("01-init-bad-deploy-mode", "01-init-no-prompt-manifest")]

    # Run non-interactive tests in parallel (quick subprocess tests)
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = {}
        for name, fn in non_interactive_tests:
            workdir = os.path.join(base_dir, name)
            os.makedirs(workdir, exist_ok=True)
            futures[pool.submit(fn, workdir)] = name
        for f in as_completed(futures):
            try:
                f.result()
            except Exception as e:
                check(futures[f], False, f"EXCEPTION: {e}")

    # Run interactive tests with limited parallelism (each uses its own tmux session)
    # Limit to 2 workers to avoid Azure API throttling when all hit same subscription
    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = {}
        for name, fn in interactive_tests:
            workdir = os.path.join(base_dir, name)
            os.makedirs(workdir, exist_ok=True)
            futures[pool.submit(fn, workdir)] = name
        for f in as_completed(futures):
            try:
                f.result()
            except Exception as e:
                check(futures[f], False, f"EXCEPTION: {e}")

    elapsed = time.time() - start

    # Summary
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    total = len(RESULTS)
    print(f"\n{'=' * 60}")
    print(f"TIER 1 RESULTS: {passed}/{total} passed ({elapsed:.1f}s)")
    print("=" * 60)

    if passed < total:
        print("\nFailed:")
        for name, ok, detail in RESULTS:
            if not ok:
                print(f"  ✗ {name}: {detail}")
        sys.exit(1)
    else:
        print("✓ ALL TIER 1 TESTS PASSED")
        sys.exit(0)
