#!/usr/bin/env python3
"""Tier 0: Offline CLI validation tests. No Azure auth needed, all parallel."""
import subprocess
import json
import os
import sys
import tempfile
import shutil
from concurrent.futures import ThreadPoolExecutor, as_completed

RESULTS = []
HOME_DIR = os.environ.get("E2E_HOME", os.environ.get("HOME", "/home/runner"))
AZD = os.environ.get("E2E_AZD", shutil.which("azd") or "azd")

# Ensure PATH includes azd location for subprocess calls
_azd_dir = os.path.dirname(AZD)
if _azd_dir and _azd_dir not in os.environ.get("PATH", ""):
    os.environ["PATH"] = f"{_azd_dir}:{os.environ.get('PATH', '')}"


def run(args, cwd=None, timeout=30):
    """Run a command and return result."""
    try:
        r = subprocess.run(args, capture_output=True, text=True, timeout=timeout, cwd=cwd)
        return r
    except subprocess.TimeoutExpired:
        return type('R', (), {'returncode': -1, 'stdout': '', 'stderr': 'TIMEOUT'})()


def check(name, passed, detail=""):
    """Record a test result. passed=True/False/None (None=SKIP)."""
    RESULTS.append((name, passed, detail))
    status = "✓" if passed else ("⊘" if passed is None else "✗")
    suffix = f" — {detail}" if detail and not passed else ""
    print(f"  {status} {name}{suffix}")
    return passed


# ============================================================
# TEST CASES
# ============================================================

def test_version():
    r = run([AZD, "ai", "agent", "version"])
    ok = r.returncode == 0 and len(r.stdout.strip()) > 0
    # Should contain a version-like string
    import re
    has_version = bool(re.search(r'\d+\.\d+', r.stdout))
    return check("00-version", ok and has_version,
                 f"exit={r.returncode}, output='{r.stdout.strip()[:50]}'")


def test_help_root():
    r = run([AZD, "ai", "agent", "--help"])
    expected_cmds = ["doctor", "init", "invoke", "monitor", "run", "show", "version"]
    missing = [c for c in expected_cmds if c not in r.stdout]
    return check("00-help-root", r.returncode == 0 and not missing,
                 f"missing: {missing}" if missing else "")


def test_sample_list_text():
    r = run([AZD, "ai", "agent", "sample", "list"])
    has_content = len(r.stdout.strip()) > 50
    has_python = "python" in r.stdout.lower() or "Python" in r.stdout
    return check("00-sample-list-text", r.returncode == 0 and has_content and has_python,
                 f"exit={r.returncode}, len={len(r.stdout)}")


def test_sample_list_json_filters():
    # Test JSON output
    r1 = run([AZD, "ai", "agent", "sample", "list", "--output", "json"])
    try:
        raw = json.loads(r1.stdout)
        # May be {"templates": [...]} or a direct list
        data = raw.get("templates", raw) if isinstance(raw, dict) else raw
        is_list = isinstance(data, list) and len(data) > 0
    except (json.JSONDecodeError, ValueError):
        return check("00-sample-list-json", False, f"invalid JSON: {r1.stdout[:100]}")

    # Test language filter
    r2 = run([AZD, "ai", "agent", "sample", "list", "--output", "json", "--language", "python"])
    try:
        raw2 = json.loads(r2.stdout)
        filtered = raw2.get("templates", raw2) if isinstance(raw2, dict) else raw2
        # Filter must return fewer results AND each must mention python
        filter_works = (isinstance(filtered, list) and
                        0 < len(filtered) < len(data) and
                        all("python" in str(item).lower() for item in filtered))
    except (json.JSONDecodeError, ValueError):
        filter_works = False

    return check("00-sample-list-json", is_list and filter_works,
                 f"all={len(data)}, python={len(filtered) if filter_works else '?'}")


def test_doctor_empty_dir():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "doctor"], cwd=td)
        # Should report something about missing azure.yaml (exit code may vary)
        output = (r.stdout + r.stderr).lower()
        mentions_missing = "azure.yaml" in output or "not found" in output or "no agent" in output
        # Accept: either non-zero exit OR mentions missing file (some versions exit 0 with warning)
        meaningful = (r.returncode != 0) or mentions_missing
        return check("00-doctor-empty-dir", meaningful,
                     f"exit={r.returncode}, mentions_missing={mentions_missing}")


def test_doctor_local_only():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "doctor", "--local-only"], cwd=td)
        # Exit code may be 0 or non-zero depending on CLI version; key is it doesn't crash
        # and produces meaningful output about the check
        output = (r.stdout + r.stderr).lower()
        not_crash = r.returncode != -1  # -1 means timeout
        has_output = len(output.strip()) > 0
        return check("00-doctor-local-only", not_crash and has_output,
                     f"exit={r.returncode}, output_hint='{(r.stdout + r.stderr).strip()[:80]}'")


def test_doctor_partial_failure():
    with tempfile.TemporaryDirectory() as td:
        # Seed minimal azure.yaml (incomplete — no agent.yaml, no services)
        with open(os.path.join(td, "azure.yaml"), "w") as f:
            f.write("name: test-agent\n")
        r = run([AZD, "ai", "agent", "doctor"], cwd=td)
        # With incomplete config, doctor should report issues
        output = (r.stdout + r.stderr).lower()
        has_diagnostic = any(k in output for k in ["fail", "error", "missing", "not found", "agent.yaml", "warn"])
        # Accept non-zero exit OR diagnostic output (some versions warn but exit 0)
        meaningful = (r.returncode != 0) or has_diagnostic
        return check("00-doctor-partial", meaningful,
                     f"exit={r.returncode}, diag={has_diagnostic}")


def test_init_validate_mutually_exclusive():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "init", "./foo.yaml", "-m", "./bar.yaml"], cwd=td)
        has_error = r.returncode != 0
        output = (r.stdout + r.stderr).lower()
        conflict_msg = "conflict" in output or "mutually exclusive" in output or "cannot" in output
        return check("00-init-mutually-exclusive", has_error and conflict_msg,
                     f"exit={r.returncode}, conflict_msg={conflict_msg}")


def test_init_no_prompt_missing():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "init", "--no-prompt"], cwd=td, timeout=15)
        # Should exit non-zero quickly, not hang
        not_hang = r.stderr != 'TIMEOUT'
        has_error = r.returncode != 0
        return check("00-init-no-prompt-missing", not_hang and has_error,
                     f"exit={r.returncode}, timeout={r.stderr == 'TIMEOUT'}")


def test_invoke_validate_protocol():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "invoke", "--protocol", "banana_protocol", "hello"], cwd=td)
        has_error = r.returncode != 0
        return check("00-invoke-bad-protocol", has_error,
                     f"exit={r.returncode}, msg='{(r.stderr or r.stdout).strip()[:80]}'")


def test_eval_context_required():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "eval", "list"], cwd=td, timeout=10)
        # Should exit non-zero (no azure.yaml / no agent context)
        has_error = r.returncode != 0 and r.stderr != 'TIMEOUT'
        output = (r.stdout + r.stderr).lower()
        mentions_context = any(k in output for k in ["azure.yaml", "not found", "error", "no agent", "context"])
        return check("00-eval-context-required", has_error and mentions_context,
                     f"exit={r.returncode}, context_msg={mentions_context}")


def test_optimize_apply_requires_candidate():
    with tempfile.TemporaryDirectory() as td:
        r = run([AZD, "ai", "agent", "optimize", "apply"], cwd=td)
        has_error = r.returncode != 0
        output = (r.stdout + r.stderr).lower()
        mentions_requirement = "candidate" in output or "required" in output or "missing" in output
        return check("00-optimize-missing-flag", has_error and mentions_requirement,
                     f"exit={r.returncode}, mentions_req={mentions_requirement}")


def test_delete_help():
    r = run([AZD, "ai", "agent", "delete", "--help"])
    has_usage = "usage" in r.stdout.lower() or "delete" in r.stdout.lower()
    return check("00-delete-help", r.returncode == 0 and has_usage,
                 f"exit={r.returncode}, len={len(r.stdout)}")


def test_endpoint_show_help():
    r = run([AZD, "ai", "agent", "endpoint", "show", "--help"])
    has_usage = "usage" in r.stdout.lower() or "endpoint" in r.stdout.lower() or "show" in r.stdout.lower()
    return check("00-endpoint-show-help", r.returncode == 0 and has_usage,
                 f"exit={r.returncode}, len={len(r.stdout)}")


def test_code_download_help():
    r = run([AZD, "ai", "agent", "code", "download", "--help"])
    has_usage = "usage" in r.stdout.lower() or "download" in r.stdout.lower()
    return check("00-code-download-help", r.returncode == 0 and has_usage,
                 f"exit={r.returncode}, len={len(r.stdout)}")


def test_init_picker_navigation():
    """This one needs tmux to test interactive picker + Ctrl-C."""
    TMUX = os.environ.get("E2E_TMUX", shutil.which("tmux") or "/usr/bin/tmux")
    SOCK = "tier0"
    SESS = "picker"
    import time

    # Skip if tmux is not available
    if not os.path.exists(TMUX):
        return check("00-init-picker-navigation", None, "tmux not found")

    # Kill old
    subprocess.run([TMUX, "-L", SOCK, "kill-server"], capture_output=True)
    time.sleep(0.3)

    with tempfile.TemporaryDirectory() as td:
        # Start tmux session
        subprocess.run([TMUX, "-L", SOCK, "new-session", "-d", "-s", SESS,
                        "-x", "200", "-y", "50", "bash --norc --noprofile"],
                       capture_output=True)
        time.sleep(1)

        def tmux_send(text):
            subprocess.run([TMUX, "-L", SOCK, "send-keys", "-t", SESS, "-l", text],
                          capture_output=True)

        def tmux_key(k):
            subprocess.run([TMUX, "-L", SOCK, "send-keys", "-t", SESS, k],
                          capture_output=True)

        def tmux_capture():
            r = subprocess.run([TMUX, "-L", SOCK, "capture-pane", "-t", SESS, "-p"],
                               capture_output=True, text=True)
            return r.stdout

        # Run init (ensure azd is in PATH — on CI it's added via GITHUB_PATH)
        azd_path = shutil.which("azd")
        azd_dir = os.path.dirname(azd_path) if azd_path else ""
        extra_paths = f"{azd_dir}:" if azd_dir else ""
        env_cmd = f"export HOME={HOME_DIR}; export PATH={extra_paths}/usr/local/bin:{HOME_DIR}/bin:/usr/bin:/bin"
        tmux_send(env_cmd)
        tmux_key("Enter")
        time.sleep(0.5)
        tmux_send(f"cd {td} && azd ai agent init")
        tmux_key("Enter")

        # Wait for picker to appear (CI runners are slower — poll up to 20s)
        has_picker = False
        for _ in range(10):
            time.sleep(2)
            cap = tmux_capture()
            if "select a language" in cap.lower():
                has_picker = True
                break

        if not has_picker:
            cap = tmux_capture()
            # Debug: print what's on screen
            print(f"    [DEBUG] tmux capture (no picker after 20s):")
            for line in cap.strip().split("\n")[-10:]:
                print(f"      | {line}")

        # Send Ctrl-C to exit
        tmux_key("C-c")
        time.sleep(2)

        # Verify Ctrl-C worked — process should have exited (shell prompt visible or tmux responsive)
        cap_after = tmux_capture()
        exited = ("$" in cap_after.split("\n")[-1] if cap_after.strip() else False) or \
                 cap_after != cap  # Screen changed after Ctrl-C

        subprocess.run([TMUX, "-L", SOCK, "kill-server"], capture_output=True)
        return check("00-init-picker-navigation", has_picker and exited,
                     f"picker={has_picker}, exited={exited}")


# ============================================================
# MAIN
# ============================================================
ALL_TESTS = [
    test_version,
    test_help_root,
    test_sample_list_text,
    test_sample_list_json_filters,
    test_doctor_empty_dir,
    test_doctor_local_only,
    test_doctor_partial_failure,
    test_init_validate_mutually_exclusive,
    test_init_no_prompt_missing,
    test_invoke_validate_protocol,
    test_eval_context_required,
    test_optimize_apply_requires_candidate,
    test_delete_help,
    test_endpoint_show_help,
    test_code_download_help,
    test_init_picker_navigation,
]

if __name__ == "__main__":
    import time
    print("=" * 60)
    print(f"TIER 0: Offline Validation ({len(ALL_TESTS)} tests)")
    print("=" * 60)

    start = time.time()

    # Run all tests in parallel (except picker which uses tmux)
    parallel_tests = ALL_TESTS[:-1]  # all except picker
    serial_tests = [ALL_TESTS[-1]]   # picker needs tmux

    with ThreadPoolExecutor(max_workers=8) as pool:
        futures = {pool.submit(t): t.__name__ for t in parallel_tests}
        for f in as_completed(futures):
            try:
                f.result()
            except Exception as e:
                check(futures[f], False, f"EXCEPTION: {e}")

    # Run picker test serially (needs tmux)
    for t in serial_tests:
        try:
            t()
        except Exception as e:
            check(t.__name__, False, f"EXCEPTION: {e}")

    elapsed = time.time() - start

    # Summary
    passed = sum(1 for _, ok, _ in RESULTS if ok is True)
    skipped = sum(1 for _, ok, _ in RESULTS if ok is None)
    failed = sum(1 for _, ok, _ in RESULTS if ok is False)
    total = len(RESULTS)
    print(f"\n{'=' * 60}")
    parts = [f"{passed} passed"]
    if skipped:
        parts.append(f"{skipped} skipped")
    if failed:
        parts.append(f"{failed} failed")
    print(f"TIER 0 RESULTS: {', '.join(parts)} (of {total}, {elapsed:.1f}s)")
    print("=" * 60)

    if failed > 0:
        print("\nFailed:")
        for name, ok, detail in RESULTS:
            if ok is False:
                print(f"  ✗ {name}: {detail}")
        sys.exit(1)
    else:
        print("✓ ALL TIER 0 TESTS PASSED" + (f" ({skipped} skipped)" if skipped else ""))
        sys.exit(0)
