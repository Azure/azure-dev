#!/usr/bin/env python3
"""Tier 2: Full E2E golden path tests — code deploy + container deploy in parallel.

Runs two instances of test_full_e2e.py simultaneously with different:
  - deploy modes (code vs container)
  - tmux session/socket names
  - working directories

Note: Agent names are derived from template defaults in separate directories.
Each instance uses its own isolated tmux socket and test directory.

Prerequisites:
  - Same as test_full_e2e.py (WSL, tmux, azd, az CLI, tokens)
  - Sufficient quota for 2 concurrent deployments
"""
import subprocess
import sys
import os
import time
import tempfile
import shutil
from concurrent.futures import ThreadPoolExecutor, as_completed

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
                subprocess.run(
                    ["azd", "down", "--force", "--purge", "--no-prompt"],
                    cwd=project_dir, env=env,
                    capture_output=True, text=True, timeout=300,
                )
                print(f"  [{label}] Cleanup complete")
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
    # Unique agent name to avoid Azure resource collisions in parallel runs
    import hashlib
    unique_suffix = hashlib.md5(f"{deploy_mode}-{os.getpid()}".encode()).hexdigest()[:6]
    env["E2E_AGENT_NAME"] = f"e2e-{deploy_mode}-{unique_suffix}"

    print(f"\n{'='*60}")
    print(f"[{label}] Starting: deploy_mode={deploy_mode}, sock={sock}")
    print(f"{'='*60}")

    start = time.time()
    try:
        r = subprocess.run(
            cmd, env=env,
            capture_output=True, text=True, timeout=1500  # 25 min max per test
        )
        elapsed = time.time() - start
        success = r.returncode == 0

        # Print output
        print(f"\n--- [{label}] Output ({elapsed:.0f}s) ---")
        lines = r.stdout.strip().split("\n")
        for line in lines[-30:]:
            print(f"  {line}")
        if r.stderr.strip():
            print(f"  [stderr] {r.stderr.strip()[:200]}")

        return {
            "label": label,
            "deploy_mode": deploy_mode,
            "success": success,
            "elapsed": elapsed,
            "returncode": r.returncode,
        }
    except subprocess.TimeoutExpired:
        elapsed = time.time() - start
        print(f"\n--- [{label}] TIMEOUT after {elapsed:.0f}s ---")
        # Attempt cleanup: find any azure.yaml and run azd down to prevent resource leak.
        _cleanup_leaked_resources(testdir, env, label)
        return {
            "label": label,
            "deploy_mode": deploy_mode,
            "success": False,
            "elapsed": elapsed,
            "returncode": -1,
        }


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(description="Tier 2: Parallel golden path E2E tests")
    parser.add_argument("--serial", action="store_true", help="Run sequentially instead of parallel")
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
    print(f"  Parallel: {not args.serial}")

    start_all = time.time()
    results = []

    if args.serial or len(tests) == 1:
        for mode, label in tests:
            result = run_e2e(mode, label)
            results.append(result)
    else:
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = {pool.submit(run_e2e, mode, label): label
                       for mode, label in tests}
            for f in as_completed(futures):
                results.append(f.result())

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
