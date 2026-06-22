#!/usr/bin/env python3
"""Tier 2: Full E2E golden path tests — code deploy + container deploy.

Runs test_full_e2e.py once per deploy mode (code, then container), sequentially.
Each run is isolated with its own:
  - deploy mode (code vs container)
  - tmux session/socket name
  - working directory
  - AZD_CONFIG_DIR (copied from ~/.azd so the installed extension is available)
  - unique agent name (avoids Azure resource collisions)

Prerequisites:
  - Same as test_full_e2e.py (WSL, tmux, azd, az CLI, tokens)
  - Model quota for one deployment at a time
"""
import subprocess
import sys
import os
import time
import tempfile
import shutil
import hashlib
import collections
import threading

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))


def _cleanup_leaked_resources(testdir, env, label):
    """Best-effort azd down for any project dirs left behind after timeout/crash."""
    if not os.path.isdir(testdir):
        return
    for d in os.listdir(testdir):
        project_dir = os.path.join(testdir, d)
        azure_yaml = os.path.join(project_dir, "azure.yaml")
        if os.path.isdir(project_dir) and os.path.isfile(azure_yaml):
            print(f"  [{label}] Cleaning up leaked resources in {project_dir}...")
            try:
                r = subprocess.run(
                    ["azd", "down", "--force", "--purge", "--no-prompt"],
                    cwd=project_dir, env=env,
                    capture_output=True, text=True, timeout=300,
                )
                if r.returncode == 0:
                    print(f"  [{label}] Cleanup complete")
                else:
                    print(f"  [{label}] Cleanup FAILED (exit {r.returncode}) — "
                          f"resources may be leaked, check the subscription")
                    if r.stderr.strip():
                        print(f"  [{label}] [stderr] {r.stderr.strip()[:300]}")
            except Exception as e:
                print(f"  [{label}] Cleanup failed: {e}")


def run_e2e(deploy_mode, label):
    """Run a full E2E test with the given deploy mode."""
    sock = f"e2e-{deploy_mode}"
    sess = f"e2e-{deploy_mode}"
    testdir = f"/tmp/e2e-tests/tier2-{deploy_mode}"

    script_path = os.path.join(SCRIPT_DIR, "test_full_e2e.py")

    cmd = [
        "python3", script_path, "--deploy-mode", deploy_mode
    ]

    env = os.environ.copy()
    env["E2E_DEPLOY_MODE"] = deploy_mode
    env["E2E_SOCK"] = sock
    env["E2E_SESS"] = sess
    env["E2E_TESTDIR"] = testdir
    # Isolate azd config per process to prevent parallel race on ~/.azd/config.json
    # Use AZD_CONFIG_DIR (not AZURE_CONFIG_DIR which is for az CLI).
    # Place outside testdir because child process rm -rf's testdir on startup.
    # Copy from default ~/.azd so extensions (installed there) are available.
    azd_config_dir = os.path.join(tempfile.gettempdir(), f"e2e-azd-config-{deploy_mode}")
    default_azd = os.path.expanduser("~/.azd")
    if os.path.isdir(default_azd):
        if os.path.isdir(azd_config_dir):
            shutil.rmtree(azd_config_dir)
        shutil.copytree(default_azd, azd_config_dir)
    else:
        os.makedirs(azd_config_dir, exist_ok=True)
    env["AZD_CONFIG_DIR"] = azd_config_dir
    # Unique agent name to avoid Azure resource collisions across runs.
    # sha256 (not md5) only to avoid noise from security scanners — this is a
    # non-cryptographic uniqueness suffix.
    unique_suffix = hashlib.sha256(f"{deploy_mode}-{os.getpid()}".encode()).hexdigest()[:6]
    env["E2E_AGENT_NAME"] = f"e2e-{deploy_mode}-{unique_suffix}"

    print(f"\n{'='*60}")
    print(f"[{label}] Starting: deploy_mode={deploy_mode}, sock={sock}")
    print(f"{'='*60}")

    timeout_s = 1500  # 25 min hard cap per test
    keep_artifacts = os.environ.get("E2E_KEEP_ARTIFACTS", "").lower() in ("1", "true", "yes")
    start = time.time()
    try:
        # Stream child output live (visible in the CI log, nothing buffered in
        # memory) while keeping a bounded tail for the summary. A watchdog timer
        # enforces the hard timeout even if the child hangs without any output.
        tail = collections.deque(maxlen=30)
        proc = subprocess.Popen(
            cmd, env=env, text=True, bufsize=1,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        assert proc.stdout is not None  # stdout=PIPE guarantees this
        timed_out = threading.Event()

        def _on_timeout():
            timed_out.set()
            proc.kill()

        watchdog = threading.Timer(timeout_s, _on_timeout)
        watchdog.start()
        try:
            for line in proc.stdout:
                sys.stdout.write(line)
                sys.stdout.flush()
                tail.append(line.rstrip("\n"))
        finally:
            watchdog.cancel()
        returncode = proc.wait()
        elapsed = time.time() - start

        if timed_out.is_set():
            print(f"\n--- [{label}] TIMEOUT after {elapsed:.0f}s ---")
            # Best-effort cleanup so a hung run does not leak Azure resources.
            _cleanup_leaked_resources(testdir, env, label)
            return {
                "label": label,
                "deploy_mode": deploy_mode,
                "success": False,
                "elapsed": elapsed,
                "returncode": -1,
            }

        print(f"\n--- [{label}] Summary ({elapsed:.0f}s, exit {returncode}) ---")
        for line in tail:
            print(f"  {line}")
        return {
            "label": label,
            "deploy_mode": deploy_mode,
            "success": returncode == 0,
            "elapsed": elapsed,
            "returncode": returncode,
        }
    finally:
        # Drop the per-mode AZD_CONFIG_DIR copy unless explicitly kept for debugging.
        if not keep_artifacts and os.path.isdir(azd_config_dir):
            shutil.rmtree(azd_config_dir, ignore_errors=True)


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(description="Tier 2: Golden path E2E tests")
    parser.add_argument("--mode", choices=["both", "code", "container"], default="both",
                        help="Which mode(s) to run")
    args = parser.parse_args()

    print("=" * 60)
    print("TIER 2: Golden Path E2E Tests")
    print("=" * 60)

    tests = []
    if args.mode in ("both", "code"):
        tests.append(("code", "CODE-DEPLOY"))
    if args.mode in ("both", "container"):
        tests.append(("container", "CONTAINER-DEPLOY"))

    print(f"  Tests: {[t[1] for t in tests]}")
    print("  Execution: sequential")

    start_all = time.time()
    results = []

    # Always run sequentially: concurrent deploys in the same subscription race
    # on shared resources (ACR, Foundry project) and exhaust model quota.
    for mode, label in tests:
        result = run_e2e(mode, label)
        results.append(result)

    total_elapsed = time.time() - start_all

    # Summary
    print(f"\n{'='*60}")
    print(f"TIER 2 RESULTS ({total_elapsed:.0f}s total)")
    print("=" * 60)
    all_pass = True
    for r in results:
        status = "✓" if r["success"] else "✗"
        print(f"  {status} {r['label']}: {'PASS' if r['success'] else 'FAIL'} ({r['elapsed']:.0f}s)")
        if not r["success"]:
            all_pass = False

    if all_pass:
        print(f"\n✓ ALL TIER 2 TESTS PASSED ({total_elapsed:.0f}s)")
        sys.exit(0)
    else:
        failed = [r["label"] for r in results if not r["success"]]
        print(f"\n✗ FAILED: {', '.join(failed)}")
        sys.exit(1)
