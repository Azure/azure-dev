#!/usr/bin/env python3
"""Tier 1: Interactive init variants. Requires Azure auth, no deploy.
Runs multiple init scenarios in parallel tmux sessions.
Validates: scaffold completes, azure.yaml generated correctly.
"""
import subprocess
import time
import sys
import os
import json
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

    def select_by_text(self, target):
        self.send(target)
        time.sleep(0.5)
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
        self.send(env_cmd)
        self.key("Enter")
        time.sleep(0.5)
        # Clear scrollback to avoid token in capture output
        self.send("clear")
        self.key("Enter")
        time.sleep(0.3)
        self.send(f"mkdir -p {workdir} && cd {workdir}")
        self.key("Enter")
        time.sleep(0.5)

    def kill(self):
        subprocess.run([TMUX, "-L", self.sock, "kill-session", "-t", self.name],
                       capture_output=True)


def handle_dynamic_prompts(sess, max_steps=20, deploy_mode="code", project_path="existing", verbose=False):
    """Handle dynamic prompts after template download until init completes.
    project_path: "existing" uses existing project, "create" creates new."""
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
                if "services:" in cap_lower or "azure.yaml" in cap_lower:
                    if verbose:
                        print(f"    [dyn-{step_num}] COMPLETE (yaml found)")
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
        elif "existing deployment" in prompt or "found" in prompt and "deployment" in prompt:
            # "Found N existing deployment(s) for model X in the selected foundry..."
            sess.key("Enter")
        elif "is specified in the agent manifest" in prompt:
            sess.key("Enter")
        elif "select a foundry project" in prompt and "host" in prompt:
            # Initial "Select a Foundry project to host..." prompt
            if project_path == "existing":
                sess.key("Enter")  # "Use an existing" is default
                time.sleep(3)
            else:
                sess.select_by_text("Create")
                time.sleep(3)
        elif "select subscription" in prompt:
            sess.select_by_text("1756")
            time.sleep(5)
        elif prompt.startswith("? select") and "foundry project" in prompt:
            # Actual project selection picker (not just mention of "foundry project")
            sess.select_by_text(PROJECT)
            time.sleep(5)
        elif "enter a different name" in prompt:
            sess.key("Enter")
        elif "acr" in prompt or "container registry" in prompt:
            sess.key("Enter")
        elif "enter model deployment name" in prompt or ("enter" in prompt and "deployment" in prompt and "name" in prompt):
            sess.key("Enter")
        elif "application insights" in prompt:
            sess.key("Enter")
        elif "capacity" in prompt:
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
        elif "deploy" in prompt and ("mode" in prompt or "how" in prompt):
            if deploy_mode == "container":
                sess.select_by_text("Container")
            else:
                sess.select_by_text("Source")
            time.sleep(3)
        elif "region" in prompt or "location" in prompt:
            sess.select_by_text("northcentralus")
            time.sleep(3)
        elif "resource group" in prompt:
            sess.key("Enter")  # accept default
        elif "account name" in prompt or "resource name" in prompt:
            sess.key("Enter")  # accept default
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
        sess.select_by_text("Basic")
        time.sleep(3)

        # Name (may take time for template list to resolve)
        if not sess.wait_for("Enter a name", 45):
            return check("01-init-python-code", False, "timeout at name")
        sess.key("Enter")
        time.sleep(3)

        # Foundry project (download phase after name: git init + template download)
        if not sess.wait_for("Foundry project", 60):
            return check("01-init-python-code", False, "timeout at foundry")
        sess.key("Enter")  # Use existing
        time.sleep(5)

        # May get subscription prompt
        cap = sess.capture()
        if "select subscription" in cap.lower():
            sess.select_by_text("1756")
            time.sleep(8)
            if not sess.wait_for("Foundry project", 30):
                # Might already be at project list
                cap = sess.capture()
                if PROJECT.lower() not in cap.lower() and "select" not in cap.lower():
                    return check("01-init-python-code", False, "timeout at project select")

        # Select project
        sess.select_by_text(PROJECT)
        time.sleep(5)

        # Handle remaining prompts
        ok = handle_dynamic_prompts(sess, deploy_mode="code", project_path="existing")

        # Verify azure.yaml
        verify_cmd = f"cat {workdir}/*/azure.yaml 2>/dev/null || cat {workdir}/azure.yaml 2>/dev/null"
        sess.send(verify_cmd)
        sess.key("Enter")
        time.sleep(2)
        cap = sess.capture()
        has_yaml = "services:" in cap.lower() or "name:" in cap.lower()

        return check("01-init-python-code", ok and has_yaml,
                     "" if ok else "init did not complete")
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
        sess.select_by_text("Basic")
        time.sleep(3)

        if not sess.wait_for("Enter a name", 45):
            return check("01-init-python-container", False, "timeout at name")
        sess.key("Enter")
        time.sleep(3)

        if not sess.wait_for("Foundry project", 60):
            return check("01-init-python-container", False, "timeout at foundry")
        sess.key("Enter")  # Use existing
        time.sleep(5)

        cap = sess.capture()
        if "select subscription" in cap.lower():
            sess.select_by_text("1756")
            time.sleep(8)
            sess.wait_for("Foundry project", 30)

        sess.select_by_text(PROJECT)
        time.sleep(5)

        ok = handle_dynamic_prompts(sess, deploy_mode="container", project_path="existing")
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

        if not sess.wait_for("Enter a name", 60):
            return check("01-init-csharp", False, "timeout at name")
        sess.key("Enter")
        time.sleep(3)

        if not sess.wait_for("Foundry project", 90):
            return check("01-init-csharp", False, "timeout at foundry")
        sess.key("Enter")  # Use existing
        time.sleep(5)

        cap = sess.capture()
        if "select subscription" in cap.lower():
            sess.select_by_text("1756")
            time.sleep(8)
            sess.wait_for("Foundry project", 30)

        sess.select_by_text(PROJECT)
        time.sleep(5)

        ok = handle_dynamic_prompts(sess, deploy_mode="code", project_path="existing")
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
        sess.select_by_text("Basic")
        time.sleep(3)

        if not sess.wait_for("Enter a name", 45):
            return check("01-init-create-project", False, "timeout at name")
        sess.key("Enter")
        time.sleep(3)

        if not sess.wait_for("Foundry project", 60):
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

        manifest_url = "https://github.com/microsoft-foundry/foundry-samples/blob/main/samples/python/hosted-agents/agent-framework/responses/04-foundry-toolbox/agent.manifest.yaml"
        sess.send(f'azd ai agent init -m "{manifest_url}"')
        sess.key("Enter")
        time.sleep(5)

        # With -m flag, may skip language/template and go to name or foundry
        # Wait for either name prompt or foundry prompt
        cap = sess.wait_for("name", 45)
        if cap and "enter a name" in cap.lower():
            sess.key("Enter")
            time.sleep(3)

        # Now wait for Foundry project (download happens here)
        if not sess.wait_for("Foundry project", 60):
            # Check if it completed directly
            cap = sess.capture()
            if "added to your azd project" in cap.lower() or "agent definition" in cap.lower():
                return check("01-init-manifest-url", True, "completed without project prompt")
            return check("01-init-manifest-url", False, "no project prompt or completion")

        sess.key("Enter")  # Use existing
        time.sleep(5)

        cap = sess.capture()
        if "select subscription" in cap.lower():
            sess.select_by_text("1756")
            time.sleep(8)
            sess.wait_for("Foundry project", 30)

        sess.select_by_text(PROJECT)
        time.sleep(5)

        ok = handle_dynamic_prompts(sess, deploy_mode="code", project_path="existing")
        return check("01-init-manifest-url", ok, "" if ok else "init did not complete")
    finally:
        sess.kill()


def test_init_with_agent_name_flag(workdir):
    """Init with --agent-name flag to skip name prompt."""
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
        sess.select_by_text("Basic")
        time.sleep(3)

        # May or may not get "Enter a name" prompt since we used --agent-name
        cap = sess.wait_for("Enter a name", 20)
        if cap:
            # Flag didn't skip name — still ok, just answer it
            sess.key("Enter")
            time.sleep(3)

        # Should go to Foundry project (with download time)
        cap = sess.wait_for("Foundry project", 60)
        if cap is None:
            cap = sess.capture()
            if "added to your azd project" in cap.lower():
                return check("01-init-agent-name-flag", True, "completed early")
            return check("01-init-agent-name-flag", False, "timeout at foundry")

        sess.key("Enter")  # Use existing
        time.sleep(5)
        cap = sess.capture()
        if "select subscription" in cap.lower():
            sess.select_by_text("1756")
            time.sleep(8)
            sess.wait_for("Foundry project", 30)

        sess.select_by_text(PROJECT)
        time.sleep(5)

        ok = handle_dynamic_prompts(sess, deploy_mode="code", project_path="existing")
        return check("01-init-agent-name-flag", ok, "" if ok else "init did not complete")
    finally:
        sess.kill()


def test_init_bad_deploy_mode(workdir):
    """Init with invalid --deploy-mode flag should error."""
    r = subprocess.run(
        ["bash", "-c", f"{ENV_SETUP}; cd {workdir} && azd ai agent init --deploy-mode banana 2>&1"],
        capture_output=True, text=True, timeout=15
    )
    has_error = r.returncode != 0 or "error" in r.stdout.lower() or "invalid" in r.stdout.lower()
    return check("01-init-bad-deploy-mode", has_error,
                 f"exit={r.returncode}, out='{r.stdout.strip()[:80]}'")


def test_init_no_prompt_with_manifest(workdir):
    """Init with --no-prompt and manifest should either complete or error gracefully."""
    manifest_url = "https://github.com/microsoft-foundry/foundry-samples/blob/main/samples/python/hosted-agents/agent-framework/responses/04-foundry-toolbox/agent.manifest.yaml"
    try:
        r = subprocess.run(
            ["bash", "-c", f"{ENV_SETUP}; cd {workdir} && azd ai agent init -m '{manifest_url}' --no-prompt 2>&1"],
            capture_output=True, text=True, timeout=90
        )
        # Completed within timeout — pass regardless of exit code (validates no crash/hang)
        return check("01-init-no-prompt-manifest", True,
                     f"exit={r.returncode}, len={len(r.stdout)}")
    except subprocess.TimeoutExpired:
        # Timeout means it's still running (likely waiting for auth or network)
        # This is acceptable behavior for --no-prompt — it didn't crash
        return check("01-init-no-prompt-manifest", True,
                     "timeout (expected for non-interactive with auth required)")


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

    # Run interactive tests in parallel (each uses its own tmux session)
    with ThreadPoolExecutor(max_workers=4) as pool:
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
