#!/usr/bin/env python3
"""Full E2E test: init -> provision -> deploy -> invoke -> down.

Prerequisites:
  - WSL with tmux, azd (with azure.ai.agents extension), az CLI
  - Caching az wrapper installed (write_az_wrapper.py)
  - Token cache pre-warmed (prewarm_tokens.py)
  - GitHub token available via gh.exe
"""
import subprocess
import time
import sys
import os
import glob as globmod

TMUX = os.environ.get("E2E_TMUX", "tmux")
SOCK = os.environ.get("E2E_SOCK", "e2e")
SESS = os.environ.get("E2E_SESS", "e2e")
TESTDIR = os.environ.get("E2E_TESTDIR", "/tmp/e2e-tests/full-e2e")
HOME_DIR = os.environ.get("E2E_HOME", os.environ.get("HOME", "/home/runner"))
ENV_SETUP = f"export HOME={HOME_DIR}; export PATH={HOME_DIR}/bin:/usr/local/bin:/usr/bin:/bin:$PATH"
SUBSCRIPTION = os.environ.get("E2E_SUBSCRIPTION", "")
PROJECT = os.environ.get("E2E_PROJECT", "")
TENANT = os.environ.get("E2E_TENANT", "")

# Track results
results = {}
DEPLOY_MODE = os.environ.get("E2E_DEPLOY_MODE", "code")  # "code" or "container"


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


def select_by_text(target):
    send(target)
    time.sleep(0.5)
    key("Enter")


def show(label="", lines_count=15):
    cap = capture()
    lines = [l for l in cap.split("\n") if l.strip()]
    if label:
        print(f"\n--- {label} ---")
    for l in lines[-lines_count:]:
        print(f"  {l}")


def wait_for_shell_prompt(timeout=600):
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


# ===========================================================
# SETUP
# ===========================================================
def setup():
    print("=" * 60)
    print("SETUP")
    print("=" * 60)

    subprocess.run([TMUX, "-L", SOCK, "kill-server"], capture_output=True)
    time.sleep(0.5)

    # Clean test dir
    subprocess.run(["rm", "-rf", TESTDIR])
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
        env_cmd += f"; export GH_TOKEN={gh_token}; export GITHUB_TOKEN={gh_token}"
        print(f"GitHub token: {len(gh_token)} chars")
    send(env_cmd)
    key("Enter")
    time.sleep(1)
    # Clear scrollback to avoid token leaking into capture output
    send("clear")
    key("Enter")
    time.sleep(0.5)

    send("echo ENV_OK")
    key("Enter")
    time.sleep(2)
    cap = capture()
    if "ENV_OK" not in cap:
        print("ERROR: Environment setup failed")
        sys.exit(1)
    print("Environment OK")

    send(f"cd {TESTDIR}")
    key("Enter")
    time.sleep(1)


# ===========================================================
# PHASE 1: INIT
# ===========================================================
def phase_init():
    print("\n" + "=" * 60)
    print("PHASE 1: azd ai agent init")
    print("=" * 60)

    send("azd ai agent init")
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
    print("[2] Template: Basic")
    select_by_text("Basic")
    time.sleep(8)

    # Step 3: Name
    if not wait_for_or_fail("Enter a name", 30, "init"):
        return False
    print("[3] Name: default")
    key("Enter")
    time.sleep(8)

    # Step 4: Foundry project type
    if not wait_for_or_fail("Select a Foundry project", 30, "init"):
        return False
    print("[4] Use existing Foundry project")
    key("Enter")
    time.sleep(8)

    # Step 5: Subscription (may be skipped)
    cap = capture()
    if "select subscription" in cap.lower():
        print("[5] Subscription: 1756")
        select_by_text("1756")
        time.sleep(8)
        if not wait_for_or_fail("Select a Foundry project", 30, "init"):
            return False
    else:
        print("[5] Subscription: skipped")

    # Step 6: Project
    print(f"[6] Project: {PROJECT}")
    select_by_text(PROJECT)
    time.sleep(10)

    # Step 7+: Dynamic prompts
    for step_num in range(7, 25):
        time.sleep(3)
        cap = capture()
        cap_lower = cap.lower()

        if "added to your azd project" in cap_lower or "agent definition added" in cap_lower:
            print(f"[{step_num}] === INIT COMPLETE ===")
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
                if "services:" in cap_lower or "azure.yaml" in cap_lower:
                    print(f"[{step_num}] Init likely complete")
                    results["init"] = "PASS"
                    return True
                print(f"[{step_num}] Shell prompt, no completion marker")
                show("Final")
                results["init"] = "FAIL (no completion)"
                return False
            print(f"[{step_num}] Waiting...")
            continue

        print(f"[{step_num}] {prompt[:80]}")

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
            print("  -> default capacity")
            key("Enter")
        elif "sku" in prompt:
            print("  -> default SKU")
            key("Enter")
        elif "version" in prompt:
            print("  -> default version")
            key("Enter")
        elif "select" in prompt and "model" in prompt:
            print("  -> select gpt")
            select_by_text("gpt")
        elif "model" in prompt:
            print("  -> default model")
            key("Enter")
        elif "deploy" in prompt and ("mode" in prompt or "how" in prompt):
            if DEPLOY_MODE == "container":
                print("  -> Container")
                select_by_text("Container")
            else:
                print("  -> Source Code")
                select_by_text("Source")
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
    cap = capture()
    # Extract project dir from init output
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
    send(f"cd {project_dir}")
    key("Enter")
    time.sleep(1)

    send("azd provision --no-prompt")
    key("Enter")
    time.sleep(5)

    # Provision can take several minutes
    print("Waiting for provision to complete (up to 10 min)...")
    cap = wait_for_shell_prompt(timeout=600)
    if cap is None:
        print("TIMEOUT: provision did not complete in 10 min")
        show("Current state", 20)
        results["provision"] = "FAIL (timeout)"
        return False

    show("Provision result", 20)
    cap_lower = cap.lower()
    if "error" in cap_lower and "success" not in cap_lower:
        print("Provision FAILED")
        results["provision"] = "FAIL"
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

    send("azd deploy --no-prompt")
    key("Enter")
    time.sleep(5)

    # Deploy can take several minutes
    print("Waiting for deploy to complete (up to 10 min)...")
    cap = wait_for_shell_prompt(timeout=600)
    if cap is None:
        print("TIMEOUT: deploy did not complete in 10 min")
        show("Current state", 20)
        results["deploy"] = "FAIL (timeout)"
        return False

    show("Deploy result", 20)
    cap_lower = cap.lower()
    if "error" in cap_lower and "success" not in cap_lower:
        print("Deploy FAILED")
        results["deploy"] = "FAIL"
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

    # The invocations protocol requires JSON payload: {"message": "..."}
    # Also need service name from init
    service_name = "agent-framework-agent-basic-invocations"
    payload = '{"message": "Hello, what is 2+2?"}'

    max_retries = 3
    for attempt in range(1, max_retries + 1):
        print(f"\nInvoke attempt {attempt}/{max_retries}...")
        send(f"azd ai agent invoke {service_name} '{payload}'")
        key("Enter")
        time.sleep(5)

        cap = wait_for_shell_prompt(timeout=180)
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
        for l in lines:
            if "ERROR:" in l or ("error" in l.lower() and "500" in l):
                has_error = True
                error_msg = l.strip()
                break

        if has_error and ("500" in error_msg or "Internal Server Error" in error_msg):
            print(f"  Server error: {error_msg[:100]}")
            if attempt < max_retries:
                print(f"  Retrying in 30s (container may still be starting)...")
                time.sleep(30)
                continue
            else:
                # Get container logs for debugging
                print("\n  Fetching agent logs for debugging...")
                send(f"azd ai agent monitor {service_name} --tail 50")
                key("Enter")
                time.sleep(10)
                log_cap = wait_for_shell_prompt(timeout=60)
                if log_cap:
                    show("Agent logs", 30)
                results["invoke"] = f"FAIL (HTTP 500: {error_msg[:80]})"
                return False
        elif has_error:
            print(f"  Error: {error_msg[:100]}")
            if attempt < max_retries:
                time.sleep(15)
                continue
            results["invoke"] = f"FAIL ({error_msg[:80]})"
            return False
        else:
            # Success! Check for response content
            print("  Invoke succeeded!")
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

    send("azd down --force --purge --no-prompt")
    key("Enter")
    time.sleep(5)

    print("Waiting for teardown (up to 10 min)...")
    cap = wait_for_shell_prompt(timeout=600)
    if cap is None:
        print("TIMEOUT: teardown did not complete")
        show("Current state", 20)
        results["teardown"] = "FAIL (timeout)"
        return False

    show("Teardown result", 20)
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
    if phase_init():
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
